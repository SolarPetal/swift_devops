package deploy

import "strings"

// ShellQuote 把字符串包成 shell 单引号安全格式。
//
// 字符串里含单引号时用 '"'"' 拼接，便于远端 systemd/nohup/docker 命令复用。
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
