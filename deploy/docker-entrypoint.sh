#!/bin/sh
# docker-entrypoint.sh
# 用法：
#   docker run --rm ghcr.io/<用户名>/cfnb-go                      -> 运行一次后退出
#   docker run -d -e RUN_INTERVAL=300 ghcr.io/<用户名>/cfnb-go    -> 每 300 秒循环运行
#   docker run --rm ghcr.io/<用户名>/cfnb-go sh                   -> 进入交互 shell（参数透传）
#   docker run --rm ghcr.io/<用户名>/cfnb-go /app/cfnb --version  -> 直接执行二进制（参数透传）

set -eu

# 有额外参数时直接执行（供 CI 冒烟测试 / 调试用）
if [ "$#" -gt 0 ]; then
    exec "$@"
fi

cd /app

# 兜底检测：bind mount 的宿主路径不存在时，Docker（短语法）会把它创建成
# 同名目录，于是程序把目录当文件读写而失败——ip.txt 写失败还会连带跳过
# Cloudflare DNS 更新与 GitHub 同步。compose 已用长语法规避，这里再兜一层，
# 覆盖 docker run -v 等短语法场景，把问题在启动阶段就明确报出来。
check_not_dir() {
    if [ -d "$1" ]; then
        echo "[entrypoint] 错误：$1 是目录，不是文件。" >&2
        echo "[entrypoint] 宿主机上的 $2 很可能不存在，被 Docker 自动创建成了同名目录。" >&2
        echo "[entrypoint] 请删除该目录后重建为文件：" >&2
        echo "[entrypoint]   ip.txt      -> touch ip.txt" >&2
        echo "[entrypoint]   config.json -> git checkout -- configs/config.json" >&2
        exit 1
    fi
}
check_not_dir /app/config.json configs/config.json
check_not_dir /app/ip.txt ip.txt

INTERVAL="${RUN_INTERVAL:-0}"

if [ "$INTERVAL" -gt 0 ] 2>/dev/null; then
    echo "[entrypoint] 循环模式：每 ${INTERVAL} 秒运行一次"
    while true; do
        /app/cfnb || echo "[entrypoint] 本轮运行出现异常，继续下一轮"
        sleep "$INTERVAL"
    done
else
    echo "[entrypoint] 单次运行模式（设置 RUN_INTERVAL 可启用循环）"
    exec /app/cfnb
fi
