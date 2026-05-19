// Package web 提供前端构建产物的 embed.FS。
// 单独成包，是因为 Go embed 路径相对包目录解析且不允许 ..，
// 把它放在项目根的 web/ 目录即可直接 embed web/dist。
package web

import "embed"

//go:embed all:dist
var FS embed.FS
