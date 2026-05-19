package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// AESGCM 用 AES-256-GCM 对短文本（如 SSH 凭证）做对称加解密。
// 密文形态：base64( nonce[12] || ciphertext || tag[16] )
type AESGCM struct {
	aead cipher.AEAD
}

// NewAESGCM 从 base64 编码的 32 字节 master key 构造实例。
// key 长度必须严格为 32 字节（AES-256），否则报错。
func NewAESGCM(keyB64 string) (*AESGCM, error) {
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, fmt.Errorf("master_key 不是合法 base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("master_key 解码后必须 32 字节（AES-256），当前 %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm new: %w", err)
	}
	return &AESGCM{aead: aead}, nil
}

// Encrypt 返回 base64 编码的 (nonce || ciphertext+tag)。
// 同一明文重复加密会得到不同密文（nonce 随机）。
func (a *AESGCM) Encrypt(plain string) (string, error) {
	nonce := make([]byte, a.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("rand nonce: %w", err)
	}
	ct := a.aead.Seal(nil, nonce, []byte(plain), nil)
	blob := append(nonce, ct...)
	return base64.StdEncoding.EncodeToString(blob), nil
}

// Decrypt 接受 Encrypt 的输出，返回明文。
// 任何篡改或截断都会返回错误（GCM 自带认证）。
func (a *AESGCM) Decrypt(b64 string) (string, error) {
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	ns := a.aead.NonceSize()
	if len(blob) < ns+a.aead.Overhead() {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	plain, err := a.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("gcm open: %w", err)
	}
	return string(plain), nil
}

// GenerateMasterKey 生成一份新的 base64(32B) master key，用于 config 模板或 install.sh。
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}
