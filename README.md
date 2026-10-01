# Cloudflare IP 优选工具（Go 版）

[![CI](https://github.com/RealKiro/cfnb-go/actions/workflows/ci.yml/badge.svg)](https://github.com/RealKiro/cfnb-go/actions/workflows/ci.yml)
[![Platform](https://img.shields.io/badge/Platform-Linux%20%7C%20Windows%20%7C%20macOS-blue)]()
[![Go](https://img.shields.io/badge/Go-1.23-00ADD8?logo=go)]()
[![License](https://img.shields.io/badge/License-MIT-green)]()

> 本仓库是 **[cfnb](https://github.com/xinyitang3/cfnb)（Python 版）的 Go 语言重构版**，功能与配置项对齐，同时去除了对 `curl`、`git`、`bash`、Python 运行时的依赖。

这是一个全自动的 **Cloudflare CDN 节点优选工具**：通过 **TCP 延迟筛选** + **IP 可用性二次检测** + **HTTP 延迟及抖动检测** + **真实带宽测速** 多重机制，从多个公开数据源聚合节点，自动识别并解析任意格式（标准代码、中文名、emoji 国旗、JSON 等），筛选出当前网络环境下速度最快、延迟最低、抖动最小的 Cloudflare IP，并支持**自动更新至 Cloudflare DNS**、**通过 GitHub API 同步 ip.txt** 以及**微信实时通知**。

---

## 🚀 为什么用 Go 重写

| 维度 | Python 版 | Go 版 |
| :--- | :--- | :--- |
| 运行时依赖 | Python 3.7+ / `requests` / `aiohttp` / `brotlicffi` | **无，单一静态二进制** |
| 带宽测速 | 依赖系统 `curl` 子进程 | **Go 原生 HTTP 下载实现**（域名解析劫持等价 `curl --resolve`） |
| GitHub 同步 | 依赖 `git` 二进制 + `git_sync.sh` / `git_sync.ps1` 脚本 | **GitHub Contents API**，令牌写在 `config.json`，无脚本、无 git |
| 并发模型 | 线程池 + asyncio 混用 | goroutine + 信号量，行为一致 |
| 容器镜像 | 约 34 MB（Python 运行时 + curl + git + bash） | **6.4 MB**（Alpine + 静态二进制，实测压缩后体积） |
| 多架构构建 | arm64 需 QEMU 模拟编译，CI 约 12 分钟 | **Go 交叉编译，无需 QEMU**，CI 约 2.5 分钟 |
| 配置兼容 | `config.json` | **字段兼容，可直接沿用原有配置** |
| 测试 | 无 | `go test` 单元测试覆盖解析引擎 / 过滤 / 评分逻辑 |

配置项（`USE_GLOBAL_MODE`、`BANDWIDTH_CANDIDATES`、`BLOCKED_COUNTRIES`、广告植入、`IP_TXT_SHOW_*` 等）与 Python 版**逐项对齐**，仅有少量差异见下文。

---

## ✨ 功能特性

| 模块 | 说明 |
| :--- | :--- |
| 🌐 **多模式筛选** | 全局最优 TopN / 分国家最优 TopN |
| ⚡ **TCP 连接测试** | 并发测延迟，可设成功率阈值 |
| 🔍 **可用性二次检测** | API 验证代理能力，返回落地协议栈 |
| 🔍 **HTTP 延迟与抖动检测** | 多次探测 `/cdn-cgi/trace`，统计延迟最大值与抖动（标准差），过滤非 Cloudflare 节点 |
| 📶 **真实带宽测速** | 原生 HTTP 下载测速，实测吞吐量 |
| ⚖️ **综合加权排序** | 带宽、TCP 延迟、HTTP 延迟、抖动四项权重独立可调 |
| 🧩 **多源自适应聚合** | 支持任意格式（标准代码 / 中文名 / emoji 国旗 / JSON），统一转换 |
| ⚙️ **前置过滤（按序执行）** | TCP 测试前：端口过滤 → 黑名单过滤 → 白名单过滤 |
| 🚫 **DNS 黑名单 / IPv6 落地过滤 / IP 风险等级过滤** | 仅作用于 DNS 更新环节，风险过滤失败自动回退 |
| 🗺️ **IP 地区校准** | 基于 ipinfo.io 并发查询，Token 轮换 + 限速 + 缓存复用 |
| ☁️ **Cloudflare DNS 更新** | 原子批量替换同名 A / TXT 记录 |
| 📬 **微信实时通知** | 集成 WxPusher，异常 / 结果推送 |
| 📤 **GitHub 自动同步** | Contents API 提交 `ip.txt`，无需 git |
| 🔒 **单实例锁** | 跨平台文件锁，避免定时任务重入 |
| 🐳 **容器化** | Alpine 多阶段构建（镜像 6.4 MB），CI 自动测试并推送 GHCR（amd64 / arm64） |

---

## 📦 快速开始

### 方式一：下载预编译二进制（免编译）

前往 [Releases](https://github.com/RealKiro/cfnb-go/releases) 下载对应平台的文件：

| 平台 | 文件 |
| :--- | :--- |
| Linux x86_64 | `cfnb-<版本>-linux-amd64.tar.gz` |
| Linux ARM64 | `cfnb-<版本>-linux-arm64.tar.gz` |
| macOS Intel | `cfnb-<版本>-darwin-amd64.tar.gz` |
| macOS Apple Silicon | `cfnb-<版本>-darwin-arm64.tar.gz` |
| Windows x64 | `cfnb-<版本>-windows-amd64.zip` |
| Windows ARM64 | `cfnb-<版本>-windows-arm64.zip` |

```bash
tar -xzf cfnb-1.0.0-linux-amd64.tar.gz
cp configs/config.json .        # 程序读取二进制同目录的 config.json
./cfnb --version
```

仓库只保存源码，**编译产物一律不进仓库**（已被 `.gitignore` 排除）。发布新版本只需打标签：

```bash
git tag v1.0.1 && git push origin v1.0.1
```

CI 会自动交叉编译 6 个平台、打包并创建 Release（含 `checksums.txt`）。

### 方式二：本地运行（单二进制）

```bash
# 1. 编译（Go 1.21+）
go build -trimpath -ldflags="-s -w" -o cfnb ./cmd/cfnb

# 2. 按需修改 configs/config.json（Cloudflare / WxPusher / GitHub 令牌）

# 3. 把配置放到二进制同目录后运行一次
cp configs/config.json .
./cfnb             # Linux / macOS
cfnb.exe           # Windows

# 4. 查看版本与帮助
./cfnb --version
./cfnb --help
```

程序读取**可执行文件同目录**的 `config.json`（可用环境变量 `CFNB_CONFIG` 指定其他路径），结果写入其 `OUTPUT_FILE`（默认 `ip.txt`）。

### 方式三：Docker（推荐）

镜像由 GitHub Actions 自动构建并推送到 GHCR，支持 `amd64` / `arm64`：

```bash
# 1. 进入项目目录
cd /path/to/cfnb-go

# 2. 按需修改 configs/config.json

# 3. 确保 ip.txt 存在（用于结果持久化挂载）
touch ip.txt

# 4.（可选）使用 GHCR 镜像而非本地构建：复制模板并填入你的地址
cp deploy/.env.example deploy/.env && nano deploy/.env    # CFNB_IMAGE=ghcr.io/<你的用户名>/cfnb-go:latest

# 5. 启动（默认每 5 分钟自动运行一次；未配置 CFNB_IMAGE 时自动本地构建）
#    compose 文件位于 deploy/，构建上下文为仓库根目录
docker compose -f deploy/docker-compose.yml up -d

# 6. 查看日志
docker compose -f deploy/docker-compose.yml logs -f
```

不想用 Compose 也可以直接 `docker run`（把 `<你的用户名>` 替换为仓库所属的 GitHub 用户名）：

```bash
docker run -d --name cfnb-go \
  -e RUN_INTERVAL=300 \
  -e TZ=Asia/Shanghai \
  -v $(pwd)/configs/config.json:/app/config.json \
  -v $(pwd)/ip.txt:/app/ip.txt \
  ghcr.io/<你的用户名>/cfnb-go:latest
```

| 项 | 说明 |
| :--- | :--- |
| 镜像地址 | `ghcr.io/<你的用户名>/cfnb-go:latest`（另有 `1.0.0` / `1.0` 版本标签与 `sha-xxxxxxx` 精确提交标签） |
| 镜像来源 | `deploy/docker-compose.yml` **不写死镜像名**：通过环境变量 `CFNB_IMAGE` 注入（复制 `deploy/.env.example` 为 `deploy/.env` 填写）；未设置时回退为本地构建 |
| `RUN_INTERVAL` | 循环间隔（秒）。默认 `0` = 只运行一次；compose 默认设为 `300`（5 分钟） |
| 挂载 `configs/config.json` | 修改参数无需重建镜像；也可用 `CFNB_CONFIG` 环境变量指定容器内其他配置路径 |
| 手动运行一次 | `docker compose -f deploy/docker-compose.yml run --rm cfnb`（临时忽略循环需加 `-e RUN_INTERVAL=0`） |
| 调试 | `docker compose -f deploy/docker-compose.yml run --rm cfnb sh` |
| 自建镜像 | `docker build -f deploy/Dockerfile -t cfnb-go .`（context 须为仓库根目录） |

**Fork 用户**：CI 使用 `${{ github.repository }}` 自动定位仓库，fork 后推送到自己仓库，镜像会自动发布到 **你自己的** GHCR 命名空间（`ghcr.io/你的用户名/cfnb-go`），与原仓库互不影响：

1. fork 后在仓库的 **Actions 页面** 点击启用工作流（GitHub 默认禁用 fork 的 Actions）；
2. 推送任意提交（或手动 `Run workflow` 触发），CI 会用你自己的 `GITHUB_TOKEN` 构建并推送到你的 GHCR；
3. 首次发布的镜像包默认 **private**，如需公开拉取请到个人主页 **Packages → cfnb-go → Package settings → Change visibility** 设为 Public；
4. 复制 `deploy/.env.example` 为 `deploy/.env`，填写 `CFNB_IMAGE=ghcr.io/你的用户名/cfnb-go:latest`。

---

## ⚙️ 配置说明

全部参数位于 `configs/config.json`，文件内已带逐项注释。常用项速查：

| 参数 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `USE_GLOBAL_MODE` | `true` | `true`=全局优选；`false`=分国家优选 |
| `GLOBAL_TOP_N` / `PER_COUNTRY_TOP_N` | `15` / `1` | 两种模式的保留数量 |
| `BANDWIDTH_CANDIDATES` | `150` | 进入测速的候选节点数 |
| `MIN_SUCCESS_RATE` | `1.0` | TCP 最低成功率阈值 |
| `PRE_FILTER_PORTS` | `[443]` | TCP 测试前仅保留的端口 |
| `PRE_FILTER_BLOCKED_COUNTRIES` | `["CN"]` | 前置黑名单（测试前剔除） |
| `ALLOWED_COUNTRIES` | `["US"]` | 白名单（需 `FILTER_COUNTRIES_ENABLED: true`） |
| `HTTP_LATENCY_WEIGHT` / `JITTER_WEIGHT` / `SPEED_WEIGHT` | `3.0` | 综合排序权重 |
| `CF_ENABLED` / `DNS_RECORD_TYPE` | `true` / `TXT` | Cloudflare DNS 自动更新 |
| `DNS_UPDATE_TARGET_COUNT` | `15` | DNS 写入的最大记录数 |
| `ENABLE_WXPUSHER` | `true` | WxPusher 微信通知 |
| `MAX_WORKERS` / `BANDWIDTH_WORKERS` | `300` / `3` | 并发控制（低配设备请调小） |

综合得分公式（与 Python 版一致）：

```
得分 = (SPEED_WEIGHT × 带宽) / (1 + TCP_LATENCY_WEIGHT × TCP延迟 + HTTP_LATENCY_WEIGHT × HTTP延迟 + JITTER_WEIGHT × HTTP抖动)
```

### GitHub 自动同步（Go 版改用 API）

不再需要 `git_sync.sh` / `git_sync.ps1`，也无需在容器内安装 `git`：

```json
"GITHUB_SYNC_MAX_RETRIES": 3,
"GITHUB_TOKEN": "ghp_xxxxxxxx",
"GITHUB_OWNER": "your_username",
"GITHUB_REPO": "cf-ip",
"GITHUB_BRANCH": "main",
"GITHUB_SYNC_PATH": "ip.txt"
```

- `GITHUB_TOKEN` 需具备 `repo` 权限（classic token，建议设为 `No expiration`）
- 设置 `GITHUB_SYNC_MAX_RETRIES: 0` 即可关闭同步
- 推送后可通过 `https://raw.githubusercontent.com/<用户名>/<仓库>/refs/heads/<分支>/ip.txt` 订阅
- **建议把仓库设为 Public**，部分代理工具无法处理带 Token 的私有 Raw 链接

### 与 Python 版的差异

| 项 | 说明 |
| :--- | :--- |
| GitHub 同步 | 从 `git` 强推脚本改为 Contents API，配置从脚本变量迁移到 `config.json` |
| 带宽测速 | 从 `curl` 子进程改为原生 HTTP（域名解析劫持等价 `--resolve`），判定规则（下载量须达设定大小、速度 = 字节 × 8 / 耗时）保持一致 |
| `ENABLE_LOGGING` / `LOG_FILE` | 保留字段；日志统一输出到标准输出（容器场景更易采集） |
| `SOCKET_DEFAULT_TIMEOUT` / `BANDWIDTH_PROCESS_BUFFER` / `HTTP_TEST_MAX_RETRIES` 等 | 保留字段（Go 以连接超时 / 总超时控制，无子进程缓冲概念） |
| 二进制位置 | 无 `main.py`，编译产物为单文件 `cfnb` |

---

## 📁 目录结构

```
.
├── cmd/cfnb/                  # Go 源码（单一 main 包）
│   ├── main.go                # 入口与主流程编排（抓取 → 过滤 → 测试 → 评分 → 输出 → 更新）
│   ├── config.go              # 配置结构、默认值与加载
│   ├── countries.go           # 中文名 / 三位码 → 两位国家码映射表
│   ├── parse.go               # 自适应解析引擎（文本 / JSON / emoji / 中文）
│   ├── nettest.go             # TCP 测试、可用性检测、HTTP 检测、带宽测速
│   ├── dns.go                 # IP 风险等级查询 + Cloudflare DNS 批量更新
│   ├── ipinfo.go              # IP 地区校准（Token 轮换 / 限速 / 缓存）
│   ├── notify.go              # WxPusher 微信通知
│   ├── gitsync.go             # GitHub Contents API 同步
│   ├── output.go              # ip.txt 输出（广告行 / 指标附加）
│   ├── util.go                # HTTP 客户端、并发与进度工具
│   ├── lock_unix.go           # 单实例锁（flock）
│   ├── lock_windows.go        # 单实例锁（Windows 独占句柄）
│   └── parse_test.go          # 单元测试
├── configs/
│   └── config.json            # 配置文件（含逐项注释）
├── deploy/                    # 部署相关
│   ├── Dockerfile             # Alpine 多阶段构建
│   ├── docker-compose.yml     # 一键部署（构建上下文指向仓库根）
│   ├── docker-entrypoint.sh   # 容器入口（定时循环 / 参数透传）
│   └── .env.example           # 镜像名等环境变量模板
├── go.mod
├── README.md
└── .github/workflows/ci.yml   # CI：vet + test + 交叉编译 + 多架构推送 GHCR
```

---

## 🔄 CI/CD

每次推送 / PR 自动执行：

1. `go vet` 静态检查
2. `go test` 单元测试（解析引擎、前置过滤、综合评分等）
3. `linux/amd64` 与 `linux/arm64` 交叉编译验证 + 二进制 `--version` 冒烟测试
4. 推送到 main 分支时：多架构构建镜像并推送 GHCR（PR 仅测试，不推送）

---

## ❓ 常见问题

1. **容器内跑完一次就退出了？** 默认 `RUN_INTERVAL=0` 为单次运行；`docker compose` 已默认设为 300 秒循环。
2. **带宽测速全部失败？** 程序会降级使用 TCP 排序结果并发送微信通知；可适当调大 `BANDWIDTH_TIMEOUT`、降低 `BANDWIDTH_SIZE_MB`。
3. **TCP 测试无节点通过？** 这是第一道硬门槛（无回退）：检查网络能否直连，或降低 `MIN_SUCCESS_RATE`。
4. **DNS 更新记录数少于 `DNS_UPDATE_TARGET_COUNT`？** 属正常现象：端口 / IPv6 落地 / 黑名单 / 风险等级过滤会剔除部分节点，可通过增大 `BANDWIDTH_CANDIDATES` 扩大候选池。
5. **提示"检测到本程序已在运行"？** 单实例锁生效，避免定时任务重叠；锁文件为程序同目录 `.run.lock`。
6. **IP 地区校准很慢？** 调低 `IP_CALIBRATION_CONCURRENCY` 或增大 `IP_CALIBRATION_MIN_INTERVAL`；不使用则设 `IP_CALIBRATION_ENABLED: false`。
7. **代理环境影响？** 与 Python 版一致：TCP / HTTP / 测速阶段强制直连，API 类请求（抓取、可用性、通知、GitHub）跟随系统代理；`FORCE_DIRECT: true` 可全部直连。

---

## 🙏 致谢

- 原项目（Python 版）：[xinyitang3/cfnb](https://github.com/xinyitang3/cfnb)
- 节点数据源 & 检测 API：[cmliussss](https://github.com/cmliussss)
- IP 风险检测 API：[ipapi.is](https://ipapi.is/)
- IP 地区校准：[ipinfo.io](https://ipinfo.io/)
- 微信通知服务：[WxPusher](https://wxpusher.zjiecode.com/)

---

**许可证**：本项目采用 [MIT License](https://opensource.org/licenses/MIT) 开源。
