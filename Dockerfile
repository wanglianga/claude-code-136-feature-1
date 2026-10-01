# ---- 构建阶段 ----
FROM golang:1.19-alpine AS builder
WORKDIR /src
# 先拷贝依赖清单以利用构建缓存
COPY go.mod go.sum ./
RUN go mod download
# 拷贝源码（含 go:embed 的 Swagger 静态资源）
COPY . .
# Swagger UI 静态资源已签入 internal/handler/swagger，直接嵌入二进制
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/passgate .

# ---- 运行阶段 ----
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser
WORKDIR /app
COPY --from=builder /out/passgate /app/passgate
USER appuser
EXPOSE 8080
ENV REDIS_ADDR=redis:6379 \
    PORT=8080
ENTRYPOINT ["/app/passgate"]
