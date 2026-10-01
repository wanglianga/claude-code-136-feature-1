package handler

import (
	"embed"
	"io/fs"

	"github.com/gofiber/fiber/v2"
)

//go:embed swagger/swagger-ui-bundle.js swagger/swagger-ui.css swagger/favicon.png swagger/openapi.yaml swagger/index.html
var swaggerAssets embed.FS

// registerSwagger 以 go:embed 方式提供 OpenAPI 3.x 文档与 Swagger UI（无需外网/额外静态服务）
func registerSwagger(app *fiber.App) {
	sub, err := fs.Sub(swaggerAssets, "swagger")
	if err != nil {
		panic(err)
	}
	serve := func(name, contentType string) fiber.Handler {
		return func(c *fiber.Ctx) error {
			c.Set("Content-Type", contentType)
			return c.Send(mustRead(sub, name))
		}
	}
	app.Get("/openapi.yaml", serve("openapi.yaml", "text/yaml; charset=utf-8"))
	app.Get("/swagger/swagger-ui-bundle.js", serve("swagger-ui-bundle.js", "application/javascript; charset=utf-8"))
	app.Get("/swagger/swagger-ui.css", serve("swagger-ui.css", "text/css; charset=utf-8"))
	app.Get("/swagger/favicon.png", serve("favicon.png", "image/png"))
	app.Get("/swagger", func(c *fiber.Ctx) error {
		// Fiber 默认非严格路由：/swagger 与 /swagger/ 均进入此处理器
		if string(c.Request().URI().Path()) == "/swagger" {
			return c.Redirect("/swagger/", fiber.StatusMovedPermanently)
		}
		c.Set("Content-Type", "text/html; charset=utf-8")
		return c.Send(mustRead(sub, "index.html"))
	})
}

func mustRead(sub fs.FS, name string) []byte {
	b, err := fs.ReadFile(sub, name)
	if err != nil {
		panic(err)
	}
	return b
}
