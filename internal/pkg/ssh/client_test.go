package ssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	cssh "golang.org/x/crypto/ssh"
)

func genKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	block, err := cssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

func TestBuildAuth_Password(t *testing.T) {
	m, err := buildAuth(AuthMethod{Type: "password", Password: "x"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("len: %d", len(m))
	}
}

func TestBuildAuth_Key(t *testing.T) {
	keyPEM := genKeyPEM(t)
	m, err := buildAuth(AuthMethod{Type: "key", KeyPEM: keyPEM})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("len: %d", len(m))
	}
}

func TestBuildAuth_BadKey(t *testing.T) {
	_, err := buildAuth(AuthMethod{Type: "key", KeyPEM: "not a key"})
	if err == nil {
		t.Fatal("应失败")
	}
	if !strings.Contains(err.Error(), "parse private key") {
		t.Fatalf("错误信息: %v", err)
	}
}

func TestBuildAuth_UnknownType(t *testing.T) {
	if _, err := buildAuth(AuthMethod{Type: "x"}); err == nil {
		t.Fatal("应失败")
	}
}
