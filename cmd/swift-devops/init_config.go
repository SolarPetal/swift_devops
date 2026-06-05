package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	cryptopkg "swift-devops/internal/pkg/crypto"
)

type initConfigOptions struct {
	ConfigPath    string
	DataDir       string
	LogDir        string
	Port          int
	AdminUser     string
	AdminPassword string
	Force         bool
}

func runInitConfig(args []string) {
	fs := flag.NewFlagSet("init-config", flag.ExitOnError)
	opts := initConfigOptions{}
	fs.StringVar(&opts.ConfigPath, "config", "config.yaml", "config.yaml 输出路径")
	fs.StringVar(&opts.DataDir, "data-dir", "data", "数据目录")
	fs.StringVar(&opts.LogDir, "log-dir", "logs", "日志目录")
	fs.IntVar(&opts.Port, "port", 8088, "HTTP 端口")
	fs.StringVar(&opts.AdminUser, "admin-user", "admin", "管理员用户名")
	fs.StringVar(&opts.AdminPassword, "admin-password", "", "管理员初始密码；留空自动生成")
	fs.BoolVar(&opts.Force, "force", false, "覆盖已存在的配置文件")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "parse flags failed: %v\n", err)
		os.Exit(2)
	}

	generatedPassword, err := writeInitialConfig(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init config failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("config written: %s\n", opts.ConfigPath)
	if generatedPassword != "" {
		pwdFile := filepath.Join(filepath.Dir(opts.ConfigPath), "initial-admin-password.txt")
		fmt.Printf("admin username: %s\n", opts.AdminUser)
		fmt.Printf("initial password: %s\n", generatedPassword)
		fmt.Printf("password file: %s\n", pwdFile)
	}
}

func writeInitialConfig(opts initConfigOptions) (string, error) {
	if strings.TrimSpace(opts.ConfigPath) == "" {
		return "", fmt.Errorf("--config is required")
	}
	if !opts.Force {
		if _, err := os.Stat(opts.ConfigPath); err == nil {
			return "", fmt.Errorf("config already exists: %s (use --force to overwrite)", opts.ConfigPath)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	if opts.Port <= 0 || opts.Port > 65535 {
		return "", fmt.Errorf("invalid port: %d", opts.Port)
	}
	if strings.TrimSpace(opts.AdminUser) == "" {
		return "", fmt.Errorf("--admin-user is required")
	}
	if strings.TrimSpace(opts.DataDir) == "" {
		opts.DataDir = "data"
	}
	if strings.TrimSpace(opts.LogDir) == "" {
		opts.LogDir = "logs"
	}

	generatedPassword := ""
	adminPassword := opts.AdminPassword
	if adminPassword == "" {
		var err error
		adminPassword, err = randomPassword(14)
		if err != nil {
			return "", err
		}
		generatedPassword = adminPassword
	}
	hashed, err := cryptopkg.HashPassword(adminPassword)
	if err != nil {
		return "", fmt.Errorf("hash admin password: %w", err)
	}
	masterKey, err := randomBase64(32)
	if err != nil {
		return "", err
	}
	jwtSecret, err := randomBase64(32)
	if err != nil {
		return "", err
	}

	configDir := filepath.Dir(opts.ConfigPath)
	if configDir != "." && configDir != "" {
		if err := os.MkdirAll(configDir, 0o755); err != nil {
			return "", err
		}
	}
	for _, dir := range []string{
		opts.DataDir,
		filepath.Join(opts.DataDir, "artifacts"),
		filepath.Join(opts.DataDir, "build"),
		opts.LogDir,
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}

	configText := fmt.Sprintf(`server:
  port: %d
  mode: release

database:
  # 支持 sqlite / mysql / postgres；init-config 默认生成 sqlite 配置
  driver: sqlite
  dsn: %q

security:
  master_key: "%s"
  jwt_secret: "%s"
  jwt_ttl: 24h

admin:
  username: %q
  password_bcrypt: "%s"

storage:
  artifact_dir: %q
  build_workspace: %q
  max_history: 30
  max_upload_mb: 256

builder:
  docker_enabled: false

ssh:
  pool_size_per_host: 2
  idle_timeout: 5m
  connect_timeout: 10s

monitor:
  interval: 5s
  retention: 24h

log:
  level: info
  dir: %q
`,
		opts.Port,
		yamlPath(filepath.Join(opts.DataDir, "swift-devops.db")),
		masterKey,
		jwtSecret,
		opts.AdminUser,
		hashed,
		yamlPath(filepath.Join(opts.DataDir, "artifacts")),
		yamlPath(filepath.Join(opts.DataDir, "build")),
		yamlPath(opts.LogDir),
	)
	if err := os.WriteFile(opts.ConfigPath, []byte(configText), 0o600); err != nil {
		return "", err
	}
	if generatedPassword != "" {
		pwdFile := filepath.Join(filepath.Dir(opts.ConfigPath), "initial-admin-password.txt")
		if err := os.WriteFile(pwdFile, []byte(generatedPassword+"\n"), 0o600); err != nil {
			return "", err
		}
	}
	return generatedPassword, nil
}

func yamlPath(path string) string {
	return filepath.ToSlash(path)
}

func randomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

func randomPassword(n int) (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	var sb strings.Builder
	sb.Grow(n)
	max := big.NewInt(int64(len(alphabet)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteByte(alphabet[idx.Int64()])
	}
	return sb.String(), nil
}
