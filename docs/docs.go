// Package docs 内嵌 OpenAPI 规范与 Swagger UI 静态资源（离线可用）
package docs

import "embed"

// FS 根目录即 docs/：openapi.yaml、index.html、swagger-ui/*
//
//go:embed openapi.yaml index.html swagger-ui
var FS embed.FS
