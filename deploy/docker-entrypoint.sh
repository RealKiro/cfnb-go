#!/bin/sh
# docker-entrypoint.sh
# 用法：
#   docker run --rm ghcr.io/<用户名>/cfnb-go                      -> 运行一次后退出
#   docker run -d -e RUN_INTERVAL=600 ghcr.io/<用户名>/cfnb-go    -> 每 600 秒循环运行
#   docker run --rm ghcr.io/<用户名>/cfnb-go sh                   -> 进入交互 shell（参数透传）
#   docker run --rm ghcr.io/<用户名>/cfnb-go /app/cfnb --version  -> 直接执行二进制（参数透传）

set -eu

# 有额外参数时直接执行（供 CI 冒烟测试 / 调试用）
if [ "$#" -gt 0 ]; then
    exec "$@"
fi

cd /app

# 挂载点检测：bind mount 的宿主路径不存在时，Docker 会「静默把它创建成同名
# 目录」（compose 短语法与 docker run -v 都是这个行为），于是程序把目录当文件
# 读写而失败——ip.txt 写失败还会连带跳过 Cloudflare DNS 更新与 GitHub 同步。
# compose 用的是短语法，这里就是唯一的防线：发现挂载点其实是目录时，
# 在启动阶段就把问题明确报出来，而不是让程序默默跑废一整轮。
check_not_dir() {
    if [ -d "$1" ]; then
        echo "[entrypoint] 错误：$1 是目录，不是文件。" >&2
        echo "[entrypoint] 宿主机的 $2 很可能不存在，被 Docker 自动创建成了同名目录。" >&2
        echo "[entrypoint] 两个文件都随仓库分发，用 git 恢复即可：" >&2
        echo "[entrypoint]   rm -r $2 && git checkout -- $2" >&2
        exit 1
    fi
}
check_not_dir /app/config.json deploy/app/config.json
check_not_dir /app/ip.txt      deploy/app/ip.txt

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
