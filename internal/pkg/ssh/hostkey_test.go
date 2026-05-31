package ssh

import (
	"encoding/base64"
	"errors"
	"net"
	"testing"

	cssh "golang.org/x/crypto/ssh"
)

type fakeKey struct {
	raw []byte
	typ string
}

func (f fakeKey) Type() string                             { return f.typ }
func (f fakeKey) Marshal() []byte                          { return f.raw }
func (f fakeKey) Verify(_ []byte, _ *cssh.Signature) error { return nil }

func TestTOFU_FirstUseLearns(t *testing.T) {
	var learned string
	cb := tofuCallback("", &learned)
	k := fakeKey{raw: []byte{1, 2, 3, 4}, typ: "ssh-fake"}
	if err := cb("h", &net.TCPAddr{}, k); err != nil {
		t.Fatalf("首次应通过: %v", err)
	}
	want := base64.StdEncoding.EncodeToString(k.raw)
	if learned != want {
		t.Fatalf("learned: %s want %s", learned, want)
	}
}

func TestTOFU_KnownMatchPasses(t *testing.T) {
	k := fakeKey{raw: []byte{5, 6, 7, 8}, typ: "ssh-fake"}
	known := base64.StdEncoding.EncodeToString(k.raw)
	cb := tofuCallback(known, nil)
	if err := cb("h", &net.TCPAddr{}, k); err != nil {
		t.Fatalf("匹配应通过: %v", err)
	}
}

func TestTOFU_MismatchRejected(t *testing.T) {
	k := fakeKey{raw: []byte{9, 9, 9, 9}, typ: "ssh-fake"}
	cb := tofuCallback("DIFFERENT_KEY_BASE64", nil)
	err := cb("h", &net.TCPAddr{}, k)
	if err == nil {
		t.Fatal("应失败")
	}
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Fatalf("应 ErrHostKeyMismatch，得: %v", err)
	}
}

func TestTOFU_FirstUseWithNilLearned(t *testing.T) {
	cb := tofuCallback("", nil)
	k := fakeKey{raw: []byte{0xA}, typ: "ssh-fake"}
	if err := cb("h", &net.TCPAddr{}, k); err != nil {
		t.Fatalf("nil learned 指针不应崩: %v", err)
	}
}
