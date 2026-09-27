package main

import (
	"context"
	"log"
	"os"

	"github.com/gofiber/fiber/v2"
	"passgate/internal/handler"
	"passgate/internal/seed"
	"passgate/internal/service"
	"passgate/internal/store"
)

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	redisAddr := getenv("REDIS_ADDR", "127.0.0.1:6379")
	st := store.New(redisAddr, "", 0)
	ctx := context.Background()
	if err := st.Ping(ctx); err != nil {
		log.Fatalf("无法连接 Redis(%s): %v", redisAddr, err)
	}
	svc := service.New(st)
	if err := seed.SeedIfEmpty(ctx, svc); err != nil {
		log.Printf("[seed] 失败: %v", err)
	}

	app := fiber.New(fiber.Config{
		AppName:      "passgate 景区年卡核验服务",
		ErrorHandler: handler.ErrorHandler,
	})
	handler.Register(app, svc)

	port := getenv("PORT", "8080")
	log.Printf("listening on :%s (redis=%s)", port, redisAddr)
	log.Fatal(app.Listen(":" + port))
}
