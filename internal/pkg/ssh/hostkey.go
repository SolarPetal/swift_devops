package ssh

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"

	cssh "golang.org/x/crypto/ssh"
)

// ErrHostKeyMismatch 表示服务端 host key 与本地记录不一致（潜在 MITM）。
var ErrHostKeyMismatch = errors.New("host key mismatch")

// tofuCallback 构造 host key 校验回调。
//   - known 空：信任首次见到的 key，并把它写入 *learned 供调用方持久化。
//   - known 非空：必须严格匹配，否则返回 ErrHostKeyMismatch（wrap）。
//
// 调用方在 Dial 成功后应：若 known 为空且 *learned 不为空，将其落库。
func tofuCallback(known string, learned *string) cssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, key cssh.PublicKey) error {
		actual := base64.StdEncoding.EncodeToString(key.Marshal())
		if known == "" {
			if learned != nil {
				*learned = actual
			}
			return nil
		}
		if actual != known {
			return fmt.Errorf("%w: hostname=%s expected=%s actual=%s",
				ErrHostKeyMismatch, hostname, fingerprint(known), fingerprint(actual))
		}
		return nil
	}
}

func fingerprint(k string) string {
	if len(k) > 16 {
		return k[:16] + "..."
	}
	return k
}
