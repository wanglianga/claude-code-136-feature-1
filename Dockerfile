# ---- 构建阶段 ----
FROM golang:1.19-alpine AS builder

WORKDIR /app

# 先利用层缓存安装依赖
COPY go.mod go.sum ./
RUN go mod download

# 复制源码（含 docs/ 内嵌资源与 swagger-ui 资产）
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/passgate .

# ---- 运行阶段 ----
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata curl && \
    addgroup -S app && adduser -S app -G app

WORKDIR /app
COPY --from=builder /out/passgate /app/passgate
COPY README.md /app/README.md

USER app
EXPOSE 8080
# REDIS_ADDR 由 docker-compose 注入；单机 docker run 可用 --network host 或 -e REDIS_ADDR=host:6379
ENV REDIS_ADDR=redis:6379
ENV PORT=8080
ENV TZ=Asia/Shanghai

# 等待下游（compose 已用健康检查控制顺序，这里兜底）
ENTRYPOINT ["/app/passgate"]
