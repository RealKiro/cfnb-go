#!/bin/sh
# docker-entrypoint.sh
# 用法：
#   docker run --rm ghcr.io/<用户名>/cfnb-go                      -> 运行一次后退出
#   docker run -d -e RUN_INTERVAL=300 ghcr.io/<用户名>/cfnb-go    -> 每 300 秒循环运行
#   docker run --rm ghcr.io/<用户名>/cfnb-go sh                   -> 进入交互 shell（参数透传）
#   docker run --rm ghcr.io/<用户名>/cfnb-go ./cfnb --version     -> 直接执行二进制（参数透传）

set -eu

# 有额外参数时直接执行（供 CI 冒烟测试 / 调试用）
if [ "$#" -gt 0 ]; then
    exec "$@"
fi

cd /app

INTERVAL="${RUN_INTERVAL:-0}"

if [ "$INTERVAL" -gt 0 ] 2>/dev/null; then
    echo "[entrypoint] 循环模式：每 ${INTERVAL} 秒运行一次"
    while true; do
        /usr/local/bin/cfnb || echo "[entrypoint] 本轮运行出现异常，继续下一轮"
        sleep "$INTERVAL"
    done
else
    echo "[entrypoint] 单次运行模式（设置 RUN_INTERVAL 可启用循环）"
    exec /usr/local/bin/cfnb
fi
