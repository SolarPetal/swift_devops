package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerCfg   `yaml:"server"`
	Database DBCfg       `yaml:"database"`
	Security SecurityCfg `yaml:"security"`
	Admin    AdminCfg    `yaml:"admin"`
	Storage  StorageCfg  `yaml:"storage"`
	Builder  BuilderCfg  `yaml:"builder"`
	SSH      SSHCfg      `yaml:"ssh"`
	Monitor  MonitorCfg  `yaml:"monitor"`
	Log      LogCfg      `yaml:"log"`
}

type ServerCfg struct {
	Port int    `yaml:"port"`
	Mode string `yaml:"mode"` // release / debug
}

type DBCfg struct {
	Driver string `yaml:"driver"` // sqlite / mysql / postgres
	DSN    string `yaml:"dsn"`
}

type SecurityCfg struct {
	MasterKey string        `yaml:"master_key"` // base64 32B; used to encrypt host secrets
	JWTSecret string        `yaml:"jwt_secret"`
	JWTTTL    time.Duration `yaml:"jwt_ttl"`
}

type AdminCfg struct {
	Username       string `yaml:"username"`
	PasswordBcrypt string `yaml:"password_bcrypt"`
}

type StorageCfg struct {
	ArtifactDir    string `yaml:"artifact_dir"`
	BuildWorkspace string `yaml:"build_workspace"`
	MaxHistory     int    `yaml:"max_history"`
	MaxUploadMB    int    `yaml:"max_upload_mb"` // 单文件上限，默认 256
}

type BuilderCfg struct {
	// Deprecated: Maven 容器构建镜像功能已下线；字段保留只为兼容旧配置，当前不生效。
	DockerEnabled bool `yaml:"docker_enabled"`
	// Sprint X.9：maven_cache_dir 字段已废弃，maven 本地仓库改到 BuilderEnv.MavenLocalRepo（UI 配置）。
	// yaml 解析器对未知字段会忽略，因此移除字段不破坏老 config.yaml。
}

type SSHCfg struct {
	PoolSizePerHost int           `yaml:"pool_size_per_host"`
	IdleTimeout     time.Duration `yaml:"idle_timeout"`
	ConnectTimeout  time.Duration `yaml:"connect_timeout"`
}

type MonitorCfg struct {
	Interval  time.Duration `yaml:"interval"`
	Retention time.Duration `yaml:"retention"`
}

type LogCfg struct {
	Level string `yaml:"level"` // debug / info / warn / error
	Dir   string `yaml:"dir"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 8088
	}
	if c.Server.Mode == "" {
		c.Server.Mode = "release"
	}
	c.Database.Driver = normalizeDatabaseDriver(c.Database.Driver)
	if c.Security.JWTTTL == 0 {
		c.Security.JWTTTL = 24 * time.Hour
	}
	if c.SSH.PoolSizePerHost == 0 {
		c.SSH.PoolSizePerHost = 2
	}
	if c.SSH.IdleTimeout == 0 {
		c.SSH.IdleTimeout = 5 * time.Minute
	}
	if c.SSH.ConnectTimeout == 0 {
		c.SSH.ConnectTimeout = 10 * time.Second
	}
	if c.Monitor.Interval == 0 {
		c.Monitor.Interval = 5 * time.Second
	}
	if c.Monitor.Retention == 0 {
		c.Monitor.Retention = 24 * time.Hour
	}
	if c.Storage.MaxHistory == 0 {
		c.Storage.MaxHistory = 30
	}
	if c.Storage.MaxUploadMB == 0 {
		c.Storage.MaxUploadMB = 256
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
}

func (c *Config) validate() error {
	switch c.Database.Driver {
	case "sqlite", "mysql", "postgres":
	default:
		return fmt.Errorf("database.driver must be one of sqlite, mysql, postgres")
	}
	if c.Database.DSN == "" {
		return fmt.Errorf("database.dsn is required")
	}
	if c.Security.MasterKey == "" {
		return fmt.Errorf("security.master_key is required")
	}
	if c.Security.JWTSecret == "" {
		return fmt.Errorf("security.jwt_secret is required")
	}
	if c.Admin.Username == "" || c.Admin.PasswordBcrypt == "" {
		return fmt.Errorf("admin.username and admin.password_bcrypt are required")
	}
	if c.Storage.ArtifactDir == "" {
		return fmt.Errorf("storage.artifact_dir is required")
	}
	return nil
}

func normalizeDatabaseDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		return "sqlite"
	case "mysql":
		return "mysql"
	case "postgres", "postgresql", "pg":
		return "postgres"
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}
