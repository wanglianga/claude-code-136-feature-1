#!/usr/bin/env bash
# 一键端到端验证：重置 Redis → 重新启动服务（自动种子）→ 运行 e2e.py
set -e
cd "$(dirname "$0")/.."

echo "[1/4] 构建..."
go build -o /tmp/passgate .

echo "[2/4] 重置 Redis 并重启服务..."
redis-cli flushall > /dev/null
pkill -x passgate 2>/dev/null || true
sleep 0.5
nohup /tmp/passgate > /tmp/passgate.log 2>&1 &
for i in $(seq 1 30); do
  if curl -sf http://127.0.0.1:8080/healthz > /dev/null 2>&1; then break; fi
  sleep 0.3
done
curl -sf http://127.0.0.1:8080/healthz > /dev/null

echo "[3/4] 运行端到端验证..."
python3 test/e2e.py

echo "[4/4] 完成"
