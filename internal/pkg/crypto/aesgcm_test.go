package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func genKey(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.StdEncoding.EncodeToString(k)
}

func TestAESGCM_RoundTrip(t *testing.T) {
	a, err := NewAESGCM(genKey(t))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	cases := []string{
		"",
		"a",
		"hello world",
		"中文+特殊字符 !@#$%^&*()",
		strings.Repeat("x", 4096),
	}
	for _, in := range cases {
		ct, err := a.Encrypt(in)
		if err != nil {
			t.Fatalf("encrypt %q: %v", in, err)
		}
		out, err := a.Decrypt(ct)
		if err != nil {
			t.Fatalf("decrypt %q: %v", in, err)
		}
		if out != in {
			t.Fatalf("roundtrip mismatch: got %q want %q", out, in)
		}
	}
}

func TestAESGCM_NonceUniquenessYieldsDifferentCiphertext(t *testing.T) {
	a, _ := NewAESGCM(genKey(t))
	c1, _ := a.Encrypt("same input")
	c2, _ := a.Encrypt("same input")
	if c1 == c2 {
		t.Fatal("两次加密同明文应得到不同密文（nonce 随机），got identical")
	}
}

func TestAESGCM_TamperDetected(t *testing.T) {
	a, _ := NewAESGCM(genKey(t))
	ct, _ := a.Encrypt("secret value")
	raw, _ := base64.StdEncoding.DecodeString(ct)
	raw[len(raw)-1] ^= 0xff // 翻转 tag 末位
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := a.Decrypt(tampered); err == nil {
		t.Fatal("篡改密文后 Decrypt 应失败，实际成功")
	}
}

func TestAESGCM_WrongKeyCannotDecrypt(t *testing.T) {
	a1, _ := NewAESGCM(genKey(t))
	a2, _ := NewAESGCM(genKey(t))
	ct, _ := a1.Encrypt("secret")
	if _, err := a2.Decrypt(ct); err == nil {
		t.Fatal("用另一把 key 应解不出来，实际成功")
	}
}

func TestAESGCM_BadKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
	}{
		{"非 base64", "!!!not-base64!!!"},
		{"长度不对 16B", base64.StdEncoding.EncodeToString([]byte("only-16-bytes!!!"))},
		{"长度不对 64B", base64.StdEncoding.EncodeToString(make([]byte, 64))},
		{"空字符串", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewAESGCM(c.key); err == nil {
				t.Fatalf("应失败但成功了")
			}
		})
	}
}

func TestAESGCM_DecryptBadInput(t *testing.T) {
	a, _ := NewAESGCM(genKey(t))
	cases := []string{
		"!!!not-base64!!!",
		base64.StdEncoding.EncodeToString([]byte("short")),
		"",
	}
	for _, c := range cases {
		if _, err := a.Decrypt(c); err == nil {
			t.Fatalf("input %q 应失败但成功了", c)
		}
	}
}

func TestGenerateMasterKey(t *testing.T) {
	k1, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(k1)
	if err != nil {
		t.Fatalf("生成的不是 base64: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("应为 32 字节，实际 %d", len(raw))
	}
	// 两次应不同
	k2, _ := GenerateMasterKey()
	if k1 == k2 {
		t.Fatal("两次生成应不同")
	}
	// 生成的 key 能直接用
	if _, err := NewAESGCM(k1); err != nil {
		t.Fatalf("生成的 key 应可直接用: %v", err)
	}
}
