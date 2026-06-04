package deploy

import "testing"

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"/tmp/x":        `'/tmp/x'`,
		"/tmp/a b":      `'/tmp/a b'`,
		"/tmp/has'apos": `'/tmp/has'"'"'apos'`,
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q)=%q, want %q", in, got, want)
		}
	}
}
