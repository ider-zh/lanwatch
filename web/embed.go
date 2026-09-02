// Package web 持有前端构建产物（web/dist）的嵌入式文件系统。
// 注意：go:embed 不支持 ".."，故将 embed.go 置于 web/ 目录内，直接嵌入同级的 dist。
package web

import "embed"

// DistFS 包含 vite 构建产物（index.html + assets/）。
//
//go:embed all:dist
var DistFS embed.FS
