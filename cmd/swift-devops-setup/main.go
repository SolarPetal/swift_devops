//go:build windows_installer

package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	appName          = "swift-devops"
	displayName      = "Swift DevOps"
	serviceName      = "swift-devops"
	defaultPort      = 8088
	uninstallRegPath = `HKLM\Software\Microsoft\Windows\CurrentVersion\Uninstall\SwiftDevOps`
)

//go:embed assets/swift-devops.exe
var embeddedAssets embed.FS

func main() {
	opts := parseFlags()
	if opts.Uninstall {
		if err := uninstall(opts); err != nil {
			fatal(err)
		}
		fmt.Println("[OK] Swift DevOps 已卸载")
		return
	}
	if err := install(opts); err != nil {
		fatal(err)
	}
}

type options struct {
	InstallDir string
	DataDir    string
	Port       int
	NoStart    bool
	Uninstall  bool
	Purge      bool
}

func parseFlags() options {
	programFiles := getenvDefault("ProgramFiles", `C:\Program Files`)
	programData := getenvDefault("ProgramData", `C:\ProgramData`)
	opts := options{}
	flag.StringVar(&opts.InstallDir, "install-dir", filepath.Join(programFiles, "Swift DevOps"), "安装目录")
	flag.StringVar(&opts.DataDir, "data-dir", filepath.Join(programData, "Swift DevOps"), "数据/配置目录")
	flag.IntVar(&opts.Port, "port", defaultPort, "HTTP 端口")
	flag.BoolVar(&opts.NoStart, "no-start", false, "安装后不启动服务")
	flag.BoolVar(&opts.Uninstall, "uninstall", false, "卸载 Swift DevOps")
	flag.BoolVar(&opts.Purge, "purge", false, "卸载时同时删除数据目录")
	flag.Parse()
	return opts
}

func install(opts options) error {
	if err := requireAdmin(); err != nil {
		return err
	}
	if opts.Port <= 0 || opts.Port > 65535 {
		return fmt.Errorf("invalid port: %d", opts.Port)
	}

	configPath := filepath.Join(opts.DataDir, "config.yaml")
	dataPath := filepath.Join(opts.DataDir, "data")
	logPath := filepath.Join(opts.DataDir, "logs")
	exePath := filepath.Join(opts.InstallDir, appName+".exe")
	setupPath := filepath.Join(opts.InstallDir, appName+"-setup.exe")

	fmt.Println("[1/7] 创建目录")
	for _, dir := range []string{opts.InstallDir, opts.DataDir, dataPath, logPath} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	fmt.Println("[2/7] 安装主程序")
	if err := stopServiceIfExists(serviceName); err != nil {
		return err
	}
	if err := writeEmbeddedFile("assets/swift-devops.exe", exePath, 0o755); err != nil {
		return err
	}
	if err := copySelf(setupPath); err != nil {
		return err
	}

	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		fmt.Println("[3/7] 生成初始配置")
		if err := run(exePath, "init-config",
			"--config", configPath,
			"--data-dir", dataPath,
			"--log-dir", logPath,
			"--port", fmt.Sprint(opts.Port),
		); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		fmt.Println("[3/7] 配置已存在，跳过生成")
	}

	fmt.Println("[4/7] 注册 Windows Service")
	if err := recreateService(exePath, configPath); err != nil {
		return err
	}

	fmt.Println("[5/7] 写入开始菜单入口")
	if err := writeStartMenu(opts.InstallDir, opts.DataDir, opts.Port); err != nil {
		return err
	}

	fmt.Println("[6/7] 写入卸载信息")
	if err := writeUninstallRegistry(setupPath, opts.InstallDir); err != nil {
		return err
	}

	if opts.NoStart {
		fmt.Println("[7/7] 已跳过启动服务")
	} else {
		fmt.Println("[7/7] 启动服务")
		if err := run("sc.exe", "start", serviceName); err != nil {
			return err
		}
	}

	passwordFile := filepath.Join(opts.DataDir, "initial-admin-password.txt")
	fmt.Println()
	fmt.Println("Swift DevOps 安装完成 ✨")
	fmt.Printf("访问地址: http://127.0.0.1:%d\n", opts.Port)
	fmt.Println("管理账号: admin")
	if _, err := os.Stat(passwordFile); err == nil {
		fmt.Printf("初始密码: %s\n", passwordFile)
	}
	return nil
}

func uninstall(opts options) error {
	if err := requireAdmin(); err != nil {
		return err
	}
	fmt.Println("[1/5] 停止并删除服务")
	_ = stopServiceIfExists(serviceName)
	_ = run("sc.exe", "delete", serviceName)

	fmt.Println("[2/5] 删除开始菜单入口")
	_ = os.RemoveAll(startMenuDir())

	fmt.Println("[3/5] 删除卸载信息")
	_ = run("reg.exe", "delete", uninstallRegPath, "/f")

	fmt.Println("[4/5] 删除程序目录")
	removeInstallDirLater(opts.InstallDir)

	if opts.Purge {
		fmt.Println("[5/5] 删除数据目录")
		_ = os.RemoveAll(opts.DataDir)
	} else {
		fmt.Printf("[5/5] 保留数据目录: %s\n", opts.DataDir)
	}
	return nil
}

func requireAdmin() error {
	cmd := exec.Command("net.exe", "session")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("请右键以管理员身份运行安装包")
	}
	return nil
}

func recreateService(exePath, configPath string) error {
	_ = run("sc.exe", "delete", serviceName)
	time.Sleep(800 * time.Millisecond)
	binPath := fmt.Sprintf(`"%s" service-run --config "%s"`, exePath, configPath)
	if err := run("sc.exe", "create", serviceName,
		"binPath=", binPath,
		"DisplayName=", displayName,
		"start=", "auto",
	); err != nil {
		return err
	}
	_ = run("sc.exe", "description", serviceName, "Swift DevOps Java Service Automation Platform")
	return nil
}

func stopServiceIfExists(name string) error {
	if err := run("sc.exe", "query", name); err != nil {
		return nil
	}
	_ = run("sc.exe", "stop", name)
	time.Sleep(1500 * time.Millisecond)
	return nil
}

func writeEmbeddedFile(name, target string, perm os.FileMode) error {
	data, err := embeddedAssets.ReadFile(name)
	if err != nil {
		return err
	}
	if err := os.WriteFile(target, data, perm); err != nil {
		return err
	}
	return nil
}

func copySelf(target string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if samePath(self, target) {
		return nil
	}
	in, err := os.Open(self)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func writeStartMenu(installDir, dataDir string, port int) error {
	dir := startMenuDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	openCmd := fmt.Sprintf("@echo off\r\nstart \"\" \"http://127.0.0.1:%d\"\r\n", port)
	if err := os.WriteFile(filepath.Join(dir, "Open Swift DevOps.cmd"), []byte(openCmd), 0o644); err != nil {
		return err
	}
	serviceCmd := "@echo off\r\nservices.msc\r\n"
	if err := os.WriteFile(filepath.Join(dir, "Windows Services.cmd"), []byte(serviceCmd), 0o644); err != nil {
		return err
	}
	passwordCmd := fmt.Sprintf("@echo off\r\nnotepad \"%s\"\r\n", filepath.Join(dataDir, "initial-admin-password.txt"))
	if err := os.WriteFile(filepath.Join(dir, "Initial Admin Password.cmd"), []byte(passwordCmd), 0o644); err != nil {
		return err
	}
	uninstallCmd := fmt.Sprintf("@echo off\r\npowershell -NoProfile -ExecutionPolicy Bypass -Command \"Start-Process -Verb RunAs '%s' '--uninstall'\"\r\n",
		filepath.Join(installDir, appName+"-setup.exe"))
	return os.WriteFile(filepath.Join(dir, "Uninstall Swift DevOps.cmd"), []byte(uninstallCmd), 0o644)
}

func writeUninstallRegistry(setupPath, installDir string) error {
	uninstall := fmt.Sprintf(`"%s" --uninstall`, setupPath)
	cmds := [][]string{
		{"add", uninstallRegPath, "/v", "DisplayName", "/t", "REG_SZ", "/d", displayName, "/f"},
		{"add", uninstallRegPath, "/v", "DisplayVersion", "/t", "REG_SZ", "/d", "1.0.0", "/f"},
		{"add", uninstallRegPath, "/v", "Publisher", "/t", "REG_SZ", "/d", "Swift DevOps", "/f"},
		{"add", uninstallRegPath, "/v", "InstallLocation", "/t", "REG_SZ", "/d", installDir, "/f"},
		{"add", uninstallRegPath, "/v", "UninstallString", "/t", "REG_SZ", "/d", uninstall, "/f"},
	}
	for _, args := range cmds {
		if err := run("reg.exe", args...); err != nil {
			return err
		}
	}
	return nil
}

func removeInstallDirLater(dir string) {
	escaped := strings.ReplaceAll(dir, `"`, `\"`)
	cmd := fmt.Sprintf(`ping 127.0.0.1 -n 3 > nul & rmdir /s /q "%s"`, escaped)
	_ = exec.Command("cmd.exe", "/C", cmd).Start()
}

func startMenuDir() string {
	programData := getenvDefault("ProgramData", `C:\ProgramData`)
	return filepath.Join(programData, "Microsoft", "Windows", "Start Menu", "Programs", "Swift DevOps")
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %w\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return strings.EqualFold(a, b)
	}
	return strings.EqualFold(aa, bb)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "[ERROR]", err)
	fmt.Println("按回车退出...")
	_, _ = fmt.Scanln()
	os.Exit(1)
}
