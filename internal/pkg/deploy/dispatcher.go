// Package deploy 实现单主机部署所需的远端原语：
//   - dispatcher: SFTP 分发文件（含 MD5 校验）
//   - systemd:   单元文件渲染与 systemctl 控制
//   - health:    HTTP 健康探针
//
// 上层 service/pipeline_service 把这几块串成"上传 → 写 unit → restart → 探活"的状态机。
package deploy

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/pkg/sftp"
	cssh "golang.org/x/crypto/ssh"
)

// Dispatcher 通过 SFTP 把本地文件分发到远端。
// 每次调用都开一个独立的 SFTP session，复用上层 SSH 连接。
type Dispatcher struct {
	sc *cssh.Client
}

// NewDispatcher 接收上层（pool 或 Dial）拿到的 *ssh.Client。
func NewDispatcher(sc *cssh.Client) *Dispatcher {
	return &Dispatcher{sc: sc}
}

// MkdirAll 远端 mkdir -p（SFTP 协议级，不依赖 sh）。
func (d *Dispatcher) MkdirAll(remoteDir string) error {
	cli, err := sftp.NewClient(d.sc)
	if err != nil {
		return fmt.Errorf("open sftp: %w", err)
	}
	defer cli.Close()
	return cli.MkdirAll(remoteDir)
}

// Upload 把本地文件分块写到远端绝对路径。
//   - 自动 mkdir -p 上级目录
//   - 上传完成后远端文件 chmod 0644，便于 systemd 读取
//   - 返回本地文件 MD5（hex）；用于上层与 Artifact.FileMD5 比对
func (d *Dispatcher) Upload(localPath, remotePath string) (md5sum string, err error) {
	if !path.IsAbs(remotePath) {
		return "", fmt.Errorf("remote path must be absolute: %s", remotePath)
	}
	srcFile, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("open local: %w", err)
	}
	defer srcFile.Close()

	cli, err := sftp.NewClient(d.sc)
	if err != nil {
		return "", fmt.Errorf("open sftp: %w", err)
	}
	defer cli.Close()

	if err := cli.MkdirAll(path.Dir(remotePath)); err != nil {
		return "", fmt.Errorf("mkdir remote: %w", err)
	}

	dstFile, err := cli.Create(remotePath)
	if err != nil {
		return "", fmt.Errorf("create remote: %w", err)
	}
	// 边写边算 MD5，省一遍 IO。
	h := md5.New()
	mw := io.MultiWriter(dstFile, h)
	if _, err := io.Copy(mw, srcFile); err != nil {
		_ = dstFile.Close()
		return "", fmt.Errorf("copy: %w", err)
	}
	if err := dstFile.Close(); err != nil {
		return "", fmt.Errorf("close remote: %w", err)
	}
	if err := cli.Chmod(remotePath, 0o644); err != nil {
		return "", fmt.Errorf("chmod remote: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// WriteFile 把内容写到远端文件（用于写 systemd unit）。
//   - 自动 mkdir -p
//   - 默认 perm 0o644
func (d *Dispatcher) WriteFile(remotePath, content string, perm os.FileMode) error {
	if !path.IsAbs(remotePath) {
		return fmt.Errorf("remote path must be absolute: %s", remotePath)
	}
	if perm == 0 {
		perm = 0o644
	}
	cli, err := sftp.NewClient(d.sc)
	if err != nil {
		return fmt.Errorf("open sftp: %w", err)
	}
	defer cli.Close()
	if err := cli.MkdirAll(path.Dir(remotePath)); err != nil {
		return fmt.Errorf("mkdir remote: %w", err)
	}
	f, err := cli.Create(remotePath)
	if err != nil {
		return fmt.Errorf("create remote: %w", err)
	}
	if _, err := io.WriteString(f, content); err != nil {
		_ = f.Close()
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return cli.Chmod(remotePath, perm)
}

// LocalFileMD5 工具函数：算本地文件 MD5（hex）。
// service/artifact_service 在注册 mock 制品时使用。
func LocalFileMD5(localPath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
