// Package ssh 封装 SSH 客户端、连接池与连通性测试。
//
// 使用 golang.org/x/crypto/ssh，不依赖系统 ssh 命令。
//   - Client: 单连接，提供 Dial / Exec / Ping。
//   - Pool:   多主机连接池，Get / Put / Discard。
//   - TOFU:   host key 首次信任，之后严格匹配。
package ssh

import (
	"context"
	"fmt"
	"strings"
	"time"

	cssh "golang.org/x/crypto/ssh"
)

// AuthMethod 认证方式
type AuthMethod struct {
	Type       string // "password" / "key"
	Password   string
	KeyPEM     string
	Passphrase string // 私钥保护口令，可空
}

// HostTarget 连接目标
type HostTarget struct {
	IP           string
	Port         int
	User         string
	KnownHostKey string // base64(MarshaledKey)；空表示 TOFU 首次
}

// DialOptions 拨号选项
type DialOptions struct {
	Timeout time.Duration
}

// Client 单连接封装
type Client struct {
	sc         *cssh.Client
	learnedKey string
}

// Close 关闭底层连接
func (c *Client) Close() error { return c.sc.Close() }

// LearnedHostKey 首次 TOFU 学到的 host key（base64），其它情况下为空。
func (c *Client) LearnedHostKey() string { return c.learnedKey }

// ExecResult 命令执行结果
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec 在远端执行 cmd。
// stderr 单独捕获；exit != 0 不视为 error，只有 ctx 取消或网络挂时返回 error。
func (c *Client) Exec(ctx context.Context, cmd string) (ExecResult, error) {
	sess, err := c.sc.NewSession()
	if err != nil {
		return ExecResult{ExitCode: -1}, err
	}
	defer sess.Close()

	var outB, errB strings.Builder
	sess.Stdout = &outB
	sess.Stderr = &errB

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case e := <-done:
		r := ExecResult{Stdout: outB.String(), Stderr: errB.String()}
		if e == nil {
			return r, nil
		}
		if ee, ok := e.(*cssh.ExitError); ok {
			r.ExitCode = ee.ExitStatus()
			return r, nil
		}
		r.ExitCode = -1
		return r, e
	case <-ctx.Done():
		_ = sess.Signal(cssh.SIGKILL)
		return ExecResult{
			Stdout:   outB.String(),
			Stderr:   errB.String(),
			ExitCode: -1,
		}, ctx.Err()
	}
}

// Ping 简单验活：执行 shell builtin `:`，快且不依赖外部命令。
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Exec(ctx, ":")
	return err
}

// Dial 拨号。auth 必须是 password 或 key。
// 首次连接（KnownHostKey 为空）时信任服务端 key 并填入 Client.LearnedHostKey()。
func Dial(target HostTarget, auth AuthMethod, opts DialOptions) (*Client, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	authMethods, err := buildAuth(auth)
	if err != nil {
		return nil, err
	}
	var learned string
	cfg := &cssh.ClientConfig{
		User:            target.User,
		Auth:            authMethods,
		HostKeyCallback: tofuCallback(target.KnownHostKey, &learned),
		Timeout:         opts.Timeout,
	}
	addr := fmt.Sprintf("%s:%d", target.IP, target.Port)
	sc, err := cssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, err
	}
	return &Client{sc: sc, learnedKey: learned}, nil
}

func buildAuth(a AuthMethod) ([]cssh.AuthMethod, error) {
	switch a.Type {
	case "password":
		return []cssh.AuthMethod{cssh.Password(a.Password)}, nil
	case "key":
		var signer cssh.Signer
		var err error
		if a.Passphrase != "" {
			signer, err = cssh.ParsePrivateKeyWithPassphrase([]byte(a.KeyPEM), []byte(a.Passphrase))
		} else {
			signer, err = cssh.ParsePrivateKey([]byte(a.KeyPEM))
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		return []cssh.AuthMethod{cssh.PublicKeys(signer)}, nil
	default:
		return nil, fmt.Errorf("unknown auth type: %q", a.Type)
	}
}
