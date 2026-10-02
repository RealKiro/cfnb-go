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
| 📏 **TCP 延迟过滤（默认开）** | 候选池前剔除握手超标的节点，省下后续可用性 / IPv6 / HTTP / 带宽四段开销 |
| 🔍 **可用性二次检测** | API 验证代理能力，返回落地协议栈 |
| 🔍 **HTTP 延迟与抖动检测** | 多次探测 `/cdn-cgi/trace`，统计延迟最大值与抖动（标准差），过滤非 Cloudflare 节点 |
| 🕐 **HTTP 延迟过滤（默认开）** | 测速前剔除 HTTP 延迟超过阈值的节点，慢节点不再占用最耗时的带宽测速 |
| 📉 **抖动过滤（默认开）** | 测速前剔除抖动超过阈值的节点，提前甩掉不稳的候选 |
| 📶 **真实带宽测速** | 原生 HTTP 下载测速，实测吞吐量 |
| ⚖️ **综合加权排序** | 带宽、TCP 延迟、HTTP 延迟、抖动四项权重独立可调 |
| 🧩 **多源自适应聚合** | 支持任意格式（标准代码 / 中文名 / emoji 国旗 / JSON），裸 IP 自动补默认端口，`IP # 标签` 这类带空格的写法也能识别；源可直接写 URL，也可写 **域名**（自动 DNS 解析取 A 记录）；多源合并按 `ip:port` 去重，靠前的源优先 |
| ⚙️ **前置过滤（按序执行）** | TCP 测试前：端口过滤 → 黑名单过滤 → 白名单过滤 |
| 🚫 **DNS 黑名单 / IPv6 落地过滤 / IP 风险等级过滤** | 仅作用于 DNS 更新环节，风险过滤失败自动回退 |
| 🗺️ **IP 地区校准** | 基于 ipinfo.io 并发查询，Token 轮换 + 限速 + 缓存复用 |
| ☁️ **Cloudflare DNS 更新** | 原子批量替换同名 A / TXT 记录 |
| 📬 **微信实时通知** | 集成 WxPusher，异常 / 结果推送 |
| 📤 **GitHub 自动同步** | Contents API 提交 `ip.txt`，无需 git |
| 🔒 **单实例锁** | 跨平台文件锁，避免定时任务重入 |
| 🔬 **数据源 × 工序漏斗统计** | 每道工序按数据源打印筛减明细，结束再输出汇总矩阵；哪个源贡献多少、在哪道工序被刷掉多少一目了然，纯日志、无需配置 |
| 🧭 **日志可读性** | 每道工序、每个阶段都带图标；数字带千分位；进度在非终端（docker logs / 重定向）下按 10% 档位换行输出，不再被 `\r` 挤成一行；**数据源返回 0 个节点时会打印 HTTP 状态、内容类型、字节数与响应片段**，并自动重试 |
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

每个压缩包内含 **二进制 + `config.json`（带逐项注释的配置模板）**，解压后两者同目录，程序会自动读取：

```bash
tar -xzf cfnb-2026.10.02.9f2c767-linux-amd64.tar.gz
# 解压得到：cfnb-2026.10.02.9f2c767-linux-amd64（二进制）、config.json（配置模板）
./cfnb --version
# 首次使用前编辑同目录的 config.json（Cloudflare / WxPusher / GitHub 令牌）
./cfnb                          # 结果写入同目录的 ip.txt
```

不填任何令牌也能直接跑：程序会回退到内置默认值，只是不推送通知、不改 DNS、不同步 GitHub。

仓库只保存源码，**编译产物一律不进仓库**（已被 `.gitignore` 排除）。发布是全自动的——**推送到 `main` 即发版**：

```
push main ──> CI（测试 / 交叉编译 / 推送 GHCR）──> CI 全绿后自动发布 Release
```

- **测试没过就不发版**：发布由 CI 的最终结果驱动，红灯时不会产生 Release
- **版本号** = 日期 + 被发布提交的短 SHA（`vYYYY.MM.DD.<短SHA>`，日期取东八区），如 `v2026.10.02.9f2c767`。同一天多次发布天然不重号，且从版本号可直接反查 commit
- **只保留最新一版**：每次发版前自动删除所有旧 Release 及其 git 标签，仓库始终只有一个 Release。清理发生在编译之后、发布之前（构建失败时旧版仍在）；版本号含 commit 短 SHA、天然唯一，清理不会引起版本号复用；GHCR 上的旧版本镜像标签不受影响
- 整个发版动作无需人工介入：算版本号 → 建 tag → 交叉编译 6 个平台 → 把 `config.json` 一并打包 → 用同一版本号重建 GHCR 镜像（amd64 / arm64）→ 创建 Release（含 `checksums.txt`）
- **只想跑 CI、不发版**：让提交标题以 `[skip release]` 开头即可（锚定标题，写在正文里不影响）。只改 `README.md` / `LICENSE` 的提交本来就不会触发 CI，因此也不会发版
- 需要补发历史版本或指定版本号时，仍可手动打标签（走同一套流程）：

```bash
git tag v2026.10.02.9f2c767 && git push origin v2026.10.02.9f2c767
```

镜像标签与 Release 一一对应：`latest` 跟随 `main` 最新一次成功发版，版本标签形如 `ghcr.io/realkiro/cfnb-go:2026.10.02.9f2c767`（日期 + 提交短 SHA）。**标签与镜像内的版本号严格一致**，可直接自证：

```bash
docker run --rm --entrypoint /app/cfnb ghcr.io/realkiro/cfnb-go:2026.10.02.9f2c767 --version
# cfnb-go 2026.10.02.9f2c767
```

（`main` 每次推送还会额外产出 `sha-<短SHA>` 镜像，用于按 commit 反查；该标签内部版本即为 `sha-<短SHA>`。）

### 方式二：本地运行（单二进制）

```bash
# 1. 编译（Go 1.21+）
go build -trimpath -ldflags="-s -w" -o cfnb ./cmd/cfnb

# 2. 按需修改 deploy/app/config.json（Cloudflare / WxPusher / GitHub 令牌）

# 3. 把配置放到二进制同目录后运行一次
cp deploy/app/config.json .
./cfnb             # Linux / macOS
cfnb.exe           # Windows

# 4. 查看版本与帮助
./cfnb --version
./cfnb --help
```

程序读取**可执行文件同目录**的 `config.json`（可用环境变量 `CFNB_CONFIG` 指定其他路径），结果写入其 `OUTPUT_FILE`（默认 `ip.txt`）。

### 方式三：Docker（推荐）

镜像由 GitHub Actions 自动构建并推送到 GHCR，支持 `amd64` / `arm64`。**compose 只从 GHCR 拉取镜像，不做本地构建**，因此无需安装 Go 工具链、也不会下载 `golang` 基础镜像：

```bash
# 1. 进入 deploy 目录（compose 里的相对路径都以该文件所在目录为基准）
cd /path/to/cfnb-go/deploy

# 2. 按需修改 app/config.json（填入 Cloudflare / WxPusher / GitHub 令牌）

# 3. 启动（默认拉取 ghcr.io/realkiro/cfnb-go:latest，每 5 分钟自动运行一次）
docker compose up -d

# 4. 查看日志
docker compose logs -f
```

#### 映射目录：只需要 `deploy/app/`

宿主和容器用**同一个名字**，映射关系左右对称，一眼能对上：

| 宿主（你的硬盘） | 容器内 | 说明 |
| :--- | :--- | :--- |
| `deploy/app/config.json` | `/app/config.json` | 配置模板，仓库自带，改好令牌即可 |
| `deploy/app/ip.txt` | `/app/ip.txt` | 空白结果文件，仓库自带，程序运行时覆盖写入 |

对应 compose 中的两行：

```yaml
volumes:
  - ./app/config.json:/app/config.json
  - ./app/ip.txt:/app/ip.txt
```

三个容易绕进去的点：

- **需要存在的目录只有 `deploy/app/` 一个**（相对 `deploy/` 即 `./app/`）。它随仓库分发，克隆下来就有。
- **`/app` 是容器内部的路径，不用你创建** —— 它是镜像里的工作目录，由 Dockerfile 的 `WORKDIR` 建好。两个 `app` 只是同名，一个在宿主、一个在容器。
- **`./app/...` 的相对基准是 compose 文件所在目录**（`deploy/`），不是仓库根。在仓库根另建一个 `app/` 跟这个挂载毫无关系。

两个文件都随仓库分发，所以克隆后**无需任何额外准备**，`docker compose up -d` 直接能跑；程序退出后 `deploy/app/ip.txt` 就是优选结果。

> 两点预期行为：① `ip.txt` 每轮运行都会被重写，`git status` 会显示它变更过，属正常；② 万一这两个文件被手工删掉，Docker 会把缺失的宿主路径**静默创建成同名目录**，此时容器入口会打印修复指引后退出 —— 照提示执行 `rm -r deploy/app/xxx && git checkout -- deploy/app/xxx` 即可。

也可以从仓库根执行（compose 的相对路径仍以 `deploy/` 为基准，不会错位）：

```bash
docker compose -f deploy/docker-compose.yml up -d
```

镜像来源、拉取策略与更新频率都通过 `deploy/.env` 覆盖（默认值见表格）：

```bash
cp deploy/.env.example deploy/.env
# 用自己 fork 的镜像：CFNB_IMAGE=ghcr.io/<你的用户名>/cfnb-go:latest
# 想每次都检查镜像更新：CFNB_PULL_POLICY=always
# 改更新频率：RUN_INTERVAL=600（秒）；留空不写 = 300，即每 5 分钟一轮
```

不想用 Compose 也可以直接 `docker run`（在仓库根执行，宿主↔容器同样同名）：

```bash
docker run -d --name cfnb-go \
  -e RUN_INTERVAL=300 \
  -e TZ=Asia/Shanghai \
  -v "$(pwd)"/deploy/app/config.json:/app/config.json \
  -v "$(pwd)"/deploy/app/ip.txt:/app/ip.txt \
  ghcr.io/realkiro/cfnb-go:latest
```

> `-v` 与 compose 短语法一样，宿主路径缺失时会静默创建目录；容器入口的 `check_not_dir` 会兜住这种情况并报错退出。

| 项 | 说明 |
| :--- | :--- |
| 镜像地址 | 默认 `ghcr.io/realkiro/cfnb-go:latest`（另有版本标签如 `2026.10.02.9f2c767`——日期 + 提交短 SHA，以及 `sha-xxxxxxx` 精确提交标签） |
| 镜像来源 | 环境变量 `CFNB_IMAGE` 注入；**未设置时回退为官方镜像 `ghcr.io/realkiro/cfnb-go:latest`**，fork 用户可在 `deploy/.env` 里改成自己的地址 |
| 拉取策略 | 环境变量 `CFNB_PULL_POLICY`，默认 `missing`（本地无缓存时才拉取）。可选 `always`（每次 up 检查更新）/ `never`（只用本地已有镜像，不联网） |
| 镜像构建 | **只由 CI 构建**：compose 无 `build` 段，本地不编译镜像。需要自定义镜像时请 fork 后改代码，由 CI 推送你自己的 GHCR（`deploy/Dockerfile` 仅被 CI 引用） |
| 挂载方式 | 短语法 `./app/x:/app/x`，宿主与容器同名，只一个点。两个挂载文件都随仓库分发，开箱即用；若被手工删除，容器入口会检测到挂载点被 Docker 建成目录并打印修复指引后退出 |
| `RUN_INTERVAL` | 循环间隔（秒），**推荐用 `deploy/.env` 控制**：留空或未设置 = `300`（5 分钟）；`0` = 只运行一次。注意它是**跑完一轮后 sleep 的间隔**，所以真实周期 = 单轮耗时 + 间隔 |
| 挂载 `deploy/app/config.json` | 修改参数无需重建镜像；也可用 `CFNB_CONFIG` 环境变量指定容器内其他配置路径 |
| 手动运行一次 | `docker compose -f deploy/docker-compose.yml run --rm cfnb`（临时忽略循环需加 `-e RUN_INTERVAL=0`） |
| 调试 | `docker compose -f deploy/docker-compose.yml run --rm cfnb sh` |

**Fork 用户**：CI 使用 `${{ github.repository }}` 自动定位仓库，fork 后推送到自己仓库，镜像会自动发布到 **你自己的** GHCR 命名空间（`ghcr.io/你的用户名/cfnb-go`），与原仓库互不影响：

1. fork 后在仓库的 **Actions 页面** 点击启用工作流（GitHub 默认禁用 fork 的 Actions）；
2. 推送任意提交（或手动 `Run workflow` 触发），CI 会用你自己的 `GITHUB_TOKEN` 构建并推送到你的 GHCR；
3. 首次发布的镜像包默认 **private**，如需公开拉取请到个人主页 **Packages → cfnb-go → Package settings → Change visibility** 设为 Public；
4. 复制 `deploy/.env.example` 为 `deploy/.env`，填写 `CFNB_IMAGE=ghcr.io/你的用户名/cfnb-go:latest`。

> 第 4 步是可选的：不配置时默认使用官方镜像，同样能正常启动。

---

## ⏱️ 更新频率建议

「多久跑一轮」由 `RUN_INTERVAL` 控制（容器内循环间隔，见上一节；在 `deploy/.env` 里改，留空或未设置 = `300` 秒，即 5 分钟）。**没有唯一正确答案**，取决于你把优选出来的 IP 用在哪里：

| 使用场景 | 建议间隔 | 为什么 |
| :--- | :--- | :--- |
| 网页 / API 加速 | **15 ~ 30 分钟** | 落地路由与运营商互联质量会随时间波动，这个粒度跟得上变化，又不至于过度占用带宽 |
| 只想要一份尽量新的 IP 列表 | **1 ~ 2 小时** | 数据源本身的更新节奏没那么快，跑得更勤也筛不出更多东西 |
| 实时音视频（Jitsi / WebRTC 类） | **12 ~ 24 小时**，或固定一批 IP | 媒体流走 UDP 直连、不经 Cloudflare，优选只影响网页与信令；而**频繁切 IP 会中断正在进行的会话** |
| 一次性筛选 / 交给外部调度 | `RUN_INTERVAL=0` | 跑完一轮就退出，由 cron、systemd timer 或 CI 定时任务决定何时再跑 |

### 决定「要不要更勤」的五个因素

1. **数据源的性质**：Cloudflare 官方 anycast IP 段（`cfipv4db`、社区优选域名等）**非常稳定**，几周不变是常态；第三方反代 IP（如 `ipdb bestproxy`）随时可能下线，这类源才需要更勤地复核。
2. **DNS 生效速度**：本工具写 Cloudflare DNS 记录时用的 `CF_TTL` 默认 **60 秒**，客户端重新解析即可拿到新 IP——所以在 DNS 层面「更勤」确实能更快生效。但它救不了已经建立的连接，改不了「进行中的会话会断」这件事。
3. **单轮耗时是间隔之外的**：`RUN_INTERVAL` 是**跑完一轮后 sleep 的时长**，真实周期 = 单轮耗时 + 间隔。完整一轮要抓全部数据源（默认 8 个源、去重后约 1.6 万条）并依次跑 TCP / HTTP / 带宽测速，本身就要几十秒到几分钟。
4. **不建议低于 5 分钟**：高频运行会持续占用带宽和 CPU；上游源站（`raw.githubusercontent.com` 等）与被调用的可用性 API 也可能因此限流。
5. **GitHub 同步会留下提交记录**：开启自动同步后，每轮运行都会向仓库提交一次 `ip.txt`，间隔越短提交历史越密。

> **一句话建议**：默认的 5 分钟适合「给网页与信令做加速」这类通用场景；如果是给实时音视频用，把间隔拉到小时级、或干脆固定一批 IP，体验反而更稳。

---

## ⚙️ 配置说明

全部参数位于 `deploy/app/config.json`，文件内已带逐项注释。常用项速查：

| 参数 | 默认值 | 说明 |
| :--- | :--- | :--- |
| `USE_GLOBAL_MODE` | `true` | `true`=全局优选；`false`=分国家优选 |
| `GLOBAL_TOP_N` / `PER_COUNTRY_TOP_N` | `15` / `1` | 两种模式的保留数量 |
| `BANDWIDTH_CANDIDATES` | `300` | 进入测速的候选节点数。需 ≥ `GLOBAL_TOP_N`；默认值偏大是为了抵消 IPv6 落地过滤的筛减（见下方《为什么默认只优选 IPv4》） |
| `MIN_SUCCESS_RATE` | `1.0` | TCP 最低成功率阈值 |
| `PRE_FILTER_PORTS` | `[443]` | TCP 测试前仅保留的端口 |
| `PRE_FILTER_BLOCKED_COUNTRIES` | `["CN"]` | 前置黑名单（测试前剔除）。默认还会并入 DNS 环节的 `BLOCKED_COUNTRIES`，见下一行 |
| `PRE_FILTER_USE_DNS_BLOCKLIST` | `true` | 是否把 `BLOCKED_COUNTRIES` 并入前置黑名单。建议保持 `true`，否则名单不一致的国家要走完 TCP/HTTP/带宽三段才被淘汰 |
| `TCP_MAX_LATENCY_ENABLED` | `true` | TCP 测试后、候选池前剔除「TCP 延迟超标」的节点。见下方《TCP 延迟过滤》 |
| `TCP_MAX_LATENCY_MS` | `90.0` | TCP 延迟阈值（毫秒），**超过**即剔除（等于则保留）。仅在上一项为 `true` 时生效 |
| `PRE_BANDWIDTH_IPV6_FILTER_ENABLED` | `true` | 测速前就剔除「仅 IPv6 可达」的节点（IPv4-only 优选）。见下方《为什么默认只优选 IPv4》 |
| `PRE_BANDWIDTH_MAX_HTTP_LATENCY_ENABLED` | `true` | 测速前剔除「HTTP 延迟超标」的节点。见下方《HTTP 延迟过滤》 |
| `PRE_BANDWIDTH_MAX_HTTP_LATENCY_MS` | `180.0` | HTTP 延迟阈值（毫秒），**超过**即剔除（等于则保留）。仅在上一项为 `true` 时生效 |
| `PRE_BANDWIDTH_MAX_JITTER_ENABLED` | `true` | 测速前剔除「HTTP 抖动超标」的节点。见下方《抖动过滤》 |
| `PRE_BANDWIDTH_MAX_JITTER_MS` | `30.0` | 抖动阈值（毫秒），**超过**即剔除（等于则保留）。仅在上一项为 `true` 时生效 |
| `ALLOWED_COUNTRIES` | `["US"]` | 白名单（需 `FILTER_COUNTRIES_ENABLED: true`） |
| `HTTP_LATENCY_WEIGHT` / `JITTER_WEIGHT` / `SPEED_WEIGHT` | `3.0` | 综合排序权重 |
| `CF_ENABLED` / `DNS_RECORD_TYPE` | `true` / `TXT` | Cloudflare DNS 自动更新 |
| `DNS_UPDATE_TARGET_COUNT` | `15` | DNS 写入的最大记录数 |
| `ENABLE_WXPUSHER` | `true` | WxPusher 微信通知 |
| `MAX_WORKERS` / `BANDWIDTH_WORKERS` | `300` / `3` | 并发控制（低配设备请调小） |
| `ADDITIONAL_SOURCES` | 8 个源 | 节点数据源列表，每项 `{ "url": ..., "enabled": true }`。**多源结果按 `ip:port` 自动去重**，保留先出现的节点，因此靠前的源优先级更高。支持两种写法，见下方《数据源写法》 |
| `BARE_IP_DEFAULT_PORT` | `443` | 数据源只返回裸 IP（无端口）时补的端口；`0` = 不补并丢弃这类节点。`ipdb.api.030101.xyz`、域名直填源等都依赖此项 |
| `KEEP_UNLABELED_NODES` | `true` | 是否保留「无国家标签」的节点。见下方《为什么必须开启 `KEEP_UNLABELED_NODES`》 |

### 数据源写法

`ADDITIONAL_SOURCES` 里每项按**写法**自适应，不需要额外的类型字段：

| 写法 | 处理方式 | 例子 |
| :--- | :--- | :--- |
| 以 `http://` / `https://` 开头 | 按 URL 拉取，自适应解析纯文本 / JSON（标准代码、中文名、emoji 国旗均可） | `"https://zip.cm.edu.kg/all.txt"` |
| 其余（域名 / 裸 IP） | **直填**：对该域名做 DNS 解析，取其**全部 A 记录**当候选；IP 形式则原样使用 | `"cf.090227.xyz"`、`"cf.877774.xyz:8443"`、`"1.2.3.4"` |

直填源每次解析都可能得到不同的一批地址（社区优选域名背后是维护者动态更新的 IP），端口取源内自带端口或 `BARE_IP_DEFAULT_PORT`。

当前默认的 8 个源：

| 源 | 类型 | 说明 |
| :--- | :--- | :--- |
| `zip.cm.edu.kg/all.txt` | URL | 综合大列表（约 1.5 万条），带国家标签 |
| `countrymerge.pages.dev/all.txt` | URL | 综合列表（约 1.6 千条），带国家标签 |
| `ipdb.api.030101.xyz?type=bestproxy` | URL | 第三方反代 IP（`type=bestcf` 则为 CF 官方 IP） |
| `yuanxiawan/cfipv4db` | URL（经镜像） | 韩国 VPS 扫描的高分 IP，更新频繁，**全是 CF 官方 anycast IP**。经 `ghproxy.net` 中转，原因见下方《GitHub 源为什么走镜像》 |
| `cmliu/WorkerVless2sub` | URL（经镜像） | 整理过的优选地址列表，带国家标签。同上，经 `ghproxy.net` 中转 |
| [`cf.090227.xyz`](https://cf.090227.xyz) | 域名直填 | 老牌优选域名，三网自适应 |
| [`cf.877774.xyz`](https://cf.877774.xyz) | 域名直填 | 社区优选域名，解析出约 26 条 CF 官方 anycast IP（`104.16.148.x` / `104.16.149.x` 段） |
| [`saas.sin.fan`](https://saas.sin.fan) | 域名直填 | 社区优选域名（Singg CDN），当前仅 1 条 A 记录，净贡献小但稳定 |

### 为什么必须开启 `KEEP_UNLABELED_NODES`

这是用上 CF 官方 IP 源的**前提**，不开等于白加：

1. `cfipv4db`、`cf.090227.xyz` 等源提供的是 **Cloudflare 官方 anycast IP**（`104.16/104.17/104.19/162.159/198.41` 等段），它们**没有也能没有**国家标签——落地区域由 CF 内部路由决定。
2. 无标签节点原本会走 `AVAILABILITY_CHECK_API` 查国家。但那个 API 的语义是**「该 IP 能否作为反代」**，实测对上述 CF 官方 IP **恒返回 `success: false`**，于是节点在筛选之初就被整批丢弃。
3. 开启后，无标签节点**跳过该 API**，直接进入 TCP 与 HTTP 检测，最终由 HTTP 检测（请求 `/cdn-cgi/trace`，要求返回 `400` 且 `Server: cloudflare`）判定真伪——**这才是「是不是 CF 边缘」的实证**，验证强度不降低。

实测（本机，2026-10）：

```
104.16.144.130   400|cloudflare   TCP 0.352s
104.19.50.155    400|cloudflare   TCP 0.348s
198.41.208.128   400|cloudflare   TCP 0.222s
104.27.126.189   400|cloudflare   TCP 0.373s
150.230.206.130  400|cloudflare   TCP 1.278s   ← 第三方反代，作为对照
```

CF 官方 IP 全部通过 HTTP 检测，且 TCP 延迟明显低于第三方反代。

> ⚠️ 代价：无国家标签的节点无法参与 `BLOCKED_COUNTRIES`（仅 DNS 阶段）的国家黑名单过滤——因为不知道它落在哪。若你的场景强依赖落地国家筛选，请把此项设为 `false` 并移除 CF 官方 IP 源。

综合得分公式（与 Python 版一致）：

```
得分 = (SPEED_WEIGHT × 带宽) / (1 + TCP_LATENCY_WEIGHT × TCP延迟 + HTTP_LATENCY_WEIGHT × HTTP延迟 + JITTER_WEIGHT × HTTP抖动)
```

### 为什么默认只优选 IPv4（两个 IPv6 过滤开关的区别）

可用性 API（`AVAILABILITY_CHECK_API`）会返回每个节点的协议栈：`inferred_stack` 为 `ipv4_only` / `ipv6_only` / `dual_stack`，并附带 `supports_ipv4`、`supports_ipv6`。

**「仅 IPv6 可达」（`ipv6_only`）的节点对纯 IPv4 客户端不可用**——家用宽带、多数 VPS、大量落地机都是 IPv4-only 环境。而这类节点的**测速成绩往往很好看**（多为就近 IDC，成绩来自 IPv6 通道），于是很容易整批霸占 `ip.txt` 的前几名，形成「看着最优、拿去不能用」的假优。

程序提供两个开关，分工不同：

| 开关 | 生效位置 | 作用 |
| :--- | :--- | :--- |
| `PRE_BANDWIDTH_IPV6_FILTER_ENABLED`（默认 `true`） | 可用性检测之后、**HTTP 与带宽测速之前** | 直接剔除 `ipv6_only` 节点 |
| `FILTER_IPV6_AVAILABILITY`（默认 `true`） | **DNS 写入之前** | 兜底，再拦一次；前者生效后此处计数通常为 0 |

为什么要把过滤提前到测速之前：**带宽测速是整轮最耗时的一段**（实测约占总耗时一半），而 `ipv6_only` 节点无论如何都进不了最终 DNS 名单——晚过滤等于把一半机时花在注定被丢弃的节点上。提前之后另有两个附带好处：`ip.txt` 只留真正可用的节点，且 `ip.txt` 与写入 DNS 的名单会收敛，不再出现两套互不相干的结果。

实测（2026-10，某轮真实运行日志）：

- `ip.txt` 最终入选 10 个**全是 `#HK`**，速度 14.13 ~ 18.50 Mbps
- 同时写入 Cloudflare DNS 的 10 个却是 SG / JP / US / 无标签，速度 4.23 ~ 11.03 Mbps，**两边零重合**
- 逐个反查可用性 API：那 10 个 HK 节点里 **9 个是 `ipv6_only`**，被 DNS 环节整批剔除 → 带宽测速选出的最优批次全部作废

> ℹ️ 上面那轮「两边零重合」还有**第二条成因**：DNS 环节当时按速度排序挑节点、完全没看 `ip.txt`。这条已单独修掉，见《[DNS 写入的名单为什么与 `ip.txt` 一致](#dns-写入的名单为什么与-iptxt-一致)》——两者叠加才是那次零重合的完整解释。

> ⚠️ 若你的客户端**确实有 IPv6**，或这些节点要用于 IPv6 场景，把 `PRE_BANDWIDTH_IPV6_FILTER_ENABLED` 设为 `false` 即恢复原行为。

> ℹ️ 该过滤依赖可用性检测返回的协议栈信息。若 `TEST_AVAILABILITY` 为 `false`、或该轮可用性检测整体失败，拿不到协议栈时这一步会**自动跳过并在日志提示，不会误杀节点**。

### TCP 延迟过滤：候选池前剔除慢握手（默认开启）

**TCP 延迟**是纯 TCP 握手的往返耗时（秒），也是整条测速链路上**唯一在第一步就拿得到**的质量指标。程序在 TCP 测试之后、候选池之前加了一道闸：**延迟超过 `TCP_MAX_LATENCY_MS`（默认 `90` ms）的节点直接淘汰，连候选池都不进**。

放在这个位置的原因是省事：被它拦下的节点不再进入候选池，后续的**可用性检测 → IPv6 落地过滤 → HTTP 探测 → 带宽测速**四段开销全部省掉（其中带宽测速约占整轮耗时一半）。候选池改为从「已达标节点」里取 TCP 最优前 `BANDWIDTH_CANDIDATES` 个，日志里会写明筛选后的实际数量。

> ⚠️ **别把三个阈值互相套用。** 它们的量级与量纲都不同：⚡TCP 实测常见 `65 ~ 93 ms`，🌐HTTP（多次采样最大值、完整 HTTP 往返）常见 `180 ~ 3000 ms`，而两者在本项目里还呈**负相关**（`r ≈ −0.85`，因为 TCP 连的是最近的 anycast 边缘、HTTP 打的是该 IP 的实际服务路径）。所以「TCP 90 ms」和「HTTP 90 ms」完全不是一回事。
> ℹ️ 阈值设为 `0` 或负数视为「未设置阈值」，该道过滤自动跳过。
> 💡 这是本项目**唯一会直接削减 `BANDWIDTH_CANDIDATES` 实际入池数量**的过滤（其余三道都在入池之后生效）。若日志显示候选池被削得太薄，放宽本阈值即可。

### HTTP 延迟过滤：剔除慢节点（默认开启）

**HTTP 延迟**是同一节点多次探测 `/cdn-cgi/trace` 往返时间的**最大值**（毫秒，由 `HTTP_JITTER_SAMPLES` 次采样取最大，最少 3 次）。它既是「这个 IP 实际访问起来快不快」最直接的指标，也是综合排序惩罚项里的**主导项**（`HTTP_LATENCY_WEIGHT` 默认 `3.0`，与 `SPEED_WEIGHT` 同量级）。程序默认开启一道过滤：**延迟超过 `PRE_BANDWIDTH_MAX_HTTP_LATENCY_MS`（默认 `180` ms）的节点，在 HTTP 检测之后、带宽测速之前被剔除**。

> ⚠️ **阈值单位要先看清量级再改。** 这个延迟是**多次采样的最大值**，且走的是「直连该 IP 的完整 HTTP 往返」，量级明显高于 TCP 握手延迟：本机实测候选节点的 ⚡TCP 普遍在 `65 ~ 93 ms`，而 🌐HTTP 普遍落在 **`180 ~ 3000 ms`**。默认 `180 ms` 属于**很严**的一档——若你的网络环境下候选普遍在几百 ms，会被大量筛掉。**上线前先看日志 `🕐 延迟过滤` 那行的筛减比例**，再按实测分布调整阈值：

| HTTP 延迟 | 参考判断 |
| :--- | :--- |
| < 200 ms | 很好。本机实测的最优档（如 SG 节点 `202 ms`） |
| 200 ~ 600 ms | 正常可用 |
| > 600 ms | 偏慢。常见于绕远的 CF 官方 `104.16/104.17` 段 |
| > 1500 ms | 基本不可用，值得直接淘汰 |

> ℹ️ 阈值设为 `0` 或负数视为「未设置阈值」，该道过滤自动跳过。
> ⚠️ 该过滤依赖 HTTP 检测返回的延迟表。若 `HTTP_TEST_ENABLED` 为 `false`、或该轮 HTTP 检测整体失败（降级），拿不到延迟时这一步会**自动跳过并在日志提示，不会误杀节点**。
> 💡 与《抖动过滤》的区别：延迟淘汰「慢」，抖动淘汰「不稳」。两者可叠加；只想保留一个时，优先留抖动（区分度更大）。

### 抖动过滤：剔除不稳的节点（默认开启）

**抖动**是同一节点多次探测延迟的**标准差**（毫秒，由 `HTTP_JITTER_SAMPLES` 次 `/cdn-cgi/trace` 往返计算，最少 3 次）。程序提供一道可选过滤：**抖动超过 `PRE_BANDWIDTH_MAX_JITTER_MS` 的节点，在 HTTP 检测之后、带宽测速之前被剔除**。

为什么值得单列一道闸——它是所有指标里**区分度最大**的一个：

| 指标 | 候选节点之间的极差（实测一轮） | 权重 `3.0` 时对 penalty 的增量 |
| :--- | :--- | :--- |
| 抖动 | 13.9 ~ 1285 ms（**92.7 倍**） | 0.04 ~ 3.86 |
| HTTP 延迟 | 202 ~ 2956 ms（14.6 倍） | 0.61 ~ 8.87 |
| TCP 延迟 | 65.5 ~ 92.6 ms（1.4 倍） | 0.20 ~ 0.28 |

抖动大的节点即使勉强入选，也会被 `JITTER_WEIGHT` 与 `HTTP_LATENCY_WEIGHT` **同时**惩罚（两者相关系数约 **+1**，本质是同一个毛病）。与其让它占用最耗时的带宽测速名额，不如提前筛掉。

**为什么默认开启**：抖动是「节点稳不稳」最直接的指标，且它是所有指标里区分度最大的一个（见上表）。它唯一的风险是**单轮波动大**——实测同一个 IP 相邻两轮分别是 `1.09 ms` 与 `13.86 ms`，相差 12.7 倍。因此**上线后请看一眼日志里 `📉 抖动过滤` 那行的筛减比例**：若一刀砍掉八九成候选，把 `PRE_BANDWIDTH_MAX_JITTER_MS` 放宽到 `100 ~ 200` 即可（多数情况下放宽阈值比关掉开关更合适）。

抖动多少算高（经验分档，非代码值）：

| 抖动 | 参考判断 |
| :--- | :--- |
| < 30 ms | 很稳。默认阈值就在这一档（实测最优节点为 `4.33 ms`） |
| 50 ~ 200 ms | 偏高。网页可用，实时音视频会卡 |
| > 200 ms | 明显不稳。常见于拥塞或绕远了的 anycast 节点 |

> ℹ️ 阈值设为 `0` 或负数视为「未设置阈值」，该道过滤自动跳过。
> ⚠️ 该过滤依赖 HTTP 检测返回的抖动表。若 `HTTP_TEST_ENABLED` 为 `false`、或该轮 HTTP 检测整体失败（降级），拿不到抖动时这一步会**自动跳过并在日志提示，不会误杀节点**。
> 💡 想彻底不要波动大的节点，也可以只调 `JITTER_WEIGHT`（无需开关）——它是在排序里扣分，而这里是直接淘汰，两者可叠加使用。

### 两道国家黑名单为什么要对齐

`PRE_FILTER_BLOCKED_COUNTRIES`（TCP 测试前）与 `BLOCKED_COUNTRIES`（DNS 写入前）是两道独立的闸，都只认 `#国家` 标签。默认前者只有 `CN`、后者有 28 国，**名单一旦不一致，只会被后一道闸拦下的国家，其节点仍要走完 TCP/HTTP/带宽三段**（其中带宽测速占整轮耗时一半）才在最后被淘汰——纯属白测。

`PRE_FILTER_USE_DNS_BLOCKLIST`（默认 `true`）让前置黑名单自动并入 DNS 黑名单，从此只需维护 `BLOCKED_COUNTRIES` 一份名单。设 `false` 则回到两份名单各自独立的老行为。

前置黑名单**不误杀无标签节点**：拿不到落地国家就宁可放过（与 DNS 阶段口径一致）。因此 DNS 阶段仍可能拦下少量无标签节点，属预期行为。

### DNS 写入的名单为什么与 `ip.txt` 一致

最终优选（`ip.txt`）按**加权分**降序，而 DNS 环节此前是直接遍历「按速度降序」的测速结果取前 N 个——同一批节点存在**两套排序口径**。在「带宽通过数 > `GLOBAL_TOP_N`」时两者不仅首选不同、**集合也不同**（DNS 完全不看 `ip.txt`），实测出现过 `ip.txt` 的前 10 与写入 DNS 的 10 个**零重合**，排查时极易误判为过滤出错。

现在 DNS 环节的候选顺序以最终优选（`ip.txt` 的排列）优先，其余测速通过节点按速度降序跟在后面**作为补位池**：优选节点若被端口 / IPv6 落地 / 国家黑名单 / 风险等级过滤剔除，仍能补足 `DNS_UPDATE_TARGET_COUNT`，不会让记录数无故变少。一旦发生补位，日志会显式提示：

```
从 27 个测速节点中筛选出 10 个IP:端口 用于 DNS 更新（IPv6落地过滤(0个) + DNS黑名单过滤(0个)）。
   ↳ 名单按 ip.txt 的顺序取自最终优选（命中 8 个），另有 2 个因优选节点被上述规则拦下而从其余测速节点补位。
```

想让 DNS 只写「最快」的节点，也可以——把 `SPEED_WEIGHT` 之外的三个权重都设为 `0`，加权分就退化成速度排序，两套口径自然合一。

### GitHub 源为什么走镜像

`raw.githubusercontent.com` 在国内网络下**偶发连接重置**（容器内尤为常见）：

```
请求或解析失败 (…/high_score_ips.txt): read tcp 172.30.0.2:33938->185.199.109.133:443: read: connection reset by peer
已尝试 3 次，放弃该数据源。
```

实测出现过**三次重试全部失败**（同一时刻宿主 `curl` 同一 URL 却是 `200 / 0.5s`），所以不能只靠重试兜底。默认把这两个 GitHub 源改为经 `ghproxy.net` 中转：

```
https://ghproxy.net/https://raw.githubusercontent.com/yuanxiawan/cfipv4db/refs/heads/main/high_score_ips.txt
```

**不用 `cdn.jsdelivr.net` 的原因**：它按分支缓存，实测返回的是**明显过期的旧榜单**（与原始源内容完全对不上）；而 `ghproxy.net` 返回的内容与原始源**逐字节一致**。镜像若失效，「筛子」汇总表里该源的「抓取」列会变成 0，一眼可见。

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

## 🔬 运行日志：可读性与「数据源 × 工序」漏斗

日志整体按「阶段分节 + 图标 + 千分位数字」组织，容器里翻日志时能快速定位到某一段。

**进度行在非终端下会换行输出。** 终端里进度用 `\r` 原地刷新最紧凑，但 `docker logs`、重定向到文件、日志查看器都不是终端——`\r` 刷出来的几十帧会全部挤成一行。程序会自动识别：**stdout 不是终端时，进度改为每跨过 10% 打一行**，日志干净且不刷屏。

### 数据源 × 筛选工序漏斗

每一道工序跑完都按数据源打一行筛减明细，全部工序结束后再打一张汇总矩阵——**哪个源贡献了多少、在哪道工序被刷掉多少，一眼可查**。纯日志功能，没有开关。

```text
==============================================================
 ☁️  cfnb-go 2026.10.01.6   Cloudflare 优选节点自动筛选
==============================================================
🎯 当前模式：全局最优15个，每个节点测试 1 次 TCP 连接
🚀 带宽测速候选数：150，测速文件大小：1.0 MB，超时：3s
正在请求数据源 https://zip.cm.edu.kg/all.txt (尝试 1/3) ...
从 https://zip.cm.edu.kg/all.txt 解析出 14875 个节点。

[筛子] 📥 抓取：合计 16,617 个节点（7 个源）
       zip.cm.edu.kg           14,875
       countrymerge.pages.dev  1,667
       ...

[筛子] 🧹 去重合并：合计 16,617 → 16,513  保留 99.4%
       countrymerge.pages.dev  1,667 → 1,582
       ...

[筛子] 🚧 前置过滤：合计 16,513 → 9,686  保留 58.7%
       zip.cm.edu.kg           14,875 → 8,522
       ...

✅ TCP 测试完成！

[筛子] 🔌 TCP通过：合计 9,686 → 7,657  保留 79.1%
[筛子] 🎯 候选池：合计 7,657 → 150  保留 2.0%
[筛子] 🩺 可用通过：合计 150 → 45  保留 30.0%
[筛子] 🌐 HTTP通过：合计 45 → 36  保留 80.0%
[筛子] 🚀 带宽通过：合计 36 → 15  保留 41.7%

============ 🏆 最终优选节点 ============
🥇 38.55.199.128:443#HK   🚀 18.08 Mbps   🌐 HTTP 106.28 ms   📉 抖动 7.86 ms   ⚡ TCP 40.59 ms
🥈 149.104.27.213:443#HK   🚀 16.80 Mbps   🌐 HTTP 100.53 ms   📉 抖动 6.01 ms   ⚡ TCP 42.53 ms
 4. 149.104.29.237:443#HK   🚀 15.61 Mbps   🌐 HTTP 107.17 ms   📉 抖动 5.34 ms   ⚡ TCP 51.38 ms

============ 📊 数据源 × 筛选工序 汇总 ============
  zip.cm.edu.kg = https://zip.cm.edu.kg/all.txt
数据源                    📥抓取  🧹去重合并  🚧前置过滤  🔌TCP通过  🎯候选池  🩺可用通过  🌐HTTP通过  🚀带宽通过  🏆最终入选
-----------------------------------------------------------------------------------------------------------------------------
zip.cm.edu.kg             14,875      14,875       8,522      6,867        41          41          33          14          14
countrymerge.pages.dev     1,667       1,582       1,109        736       109           4           3           1           1
cf.090227.xyz                 27          27          27         27         0           0           0           0           0
-----------------------------------------------------------------------------------------------------------------------------
合计                      16,617      16,513       9,686      7,657       150          45          36          15          15
✅ 全部数据源本轮均有产出。
💡 每列为该工序结束后的剩余节点数，合计行即当轮总量。
```

工序顺序固定为：**📥 抓取 → 🧹 去重合并 → 🚧 前置过滤 → 🔌 TCP 通过 → 📏 TCP 延迟过滤 → 🎯 候选池 → 🩺 可用通过 → 🛡 IPv6 过滤 → 🌐 HTTP 通过 → 🕐 延迟过滤 → 📉 抖动过滤 → 🚀 带宽通过 → 🏆 最终入选 → 📡 DNS 写入**。两类工序会按配置出现或消失：可用性 / HTTP / 带宽 / DNS 在配置里关闭时不出现对应列；📏 TCP 延迟过滤仅在 `TCP_MAX_LATENCY_ENABLED` 为 `true` 时出现，🛡 IPv6 过滤仅在 `PRE_BANDWIDTH_IPV6_FILTER_ENABLED` 为 `true` 时出现，🕐 延迟过滤仅在 `PRE_BANDWIDTH_MAX_HTTP_LATENCY_ENABLED` 为 `true` 时出现，📉 抖动过滤仅在 `PRE_BANDWIDTH_MAX_JITTER_ENABLED` 为 `true` 时出现（上面的示例日志取自还没加这几道过滤的版本，故都没有对应列）。

看日志时的几个要点：

| 现象 | 含义 |
| :--- | :--- |
| 「抓取」行显示 `0` | 该源本轮抓取或解析失败（网络抖动、源改了格式），是**静默失效的排查入口**；日志会紧跟一段诊断说明原因 |
| 某行出现 `⚠️ 已清零` | 该源在自己这段工序里被刷干净了。常见于「候选池」——那里按 TCP 延迟截断取前 N，被截掉的源会集体清零，属预期行为 |
| 「抓取」数 ≫「去重合并」数 | 该源与靠前的源高度重叠。去重是**先到先得**，重复节点会归给靠前的源 |
| 某工序显示「（各数据源均无变化）」 | 该工序没刷掉任何节点（功能未启用，或条件较宽松），属正常 |
| 汇总表末尾出现 `⚠️ 以下数据源本轮未解析出任何节点` | 源站失效 / 被限流 / 网络不可达，点名列出，不用自己逐行翻 |
| 中途退出也有汇总 | 汇总由 `defer` 保证输出；即使出现「❌ 过滤后无任何有效节点，退出」这类提前返回，漏斗矩阵照常打印 |

**归属口径**：节点归属键是 `ip:port`（忽略 `#` 后的国家标签），所以**地区校准改写标签、后续工序只做删减，都不会让统计错位**；各源「去重合并」列相加恒等于当轮总量。未归入任何已知源的节点（例如 DNS 降级路径里的裸 IP）不计入任何源，只体现在总数中。

源标签自动缩写以便对齐：普通 URL 取主机名（`zip.cm.edu.kg`），GitHub raw 取 `owner/repo`，域名直填源用原名（`cf.090227.xyz`）；撞车时依次退化为「主机名/末段路径」和完整 URL，缩写的原始地址会以图例形式列在汇总表上方。

### 数据源解析出 0 个节点怎么办

「源站明明有数据，却解析出 0 个节点」是这类工具最难查的问题——程序那句 `解析出 0 个节点` 本身不提供任何线索。现在遇到这种情况会直接把**拿到的到底是什么**打给你，并且按抓取失败重试：

```text
正在请求数据源 https://example.com/all.txt (尝试 1/3) ...
⚠️  https://example.com/all.txt 请求成功（HTTP 200，text/html，4.1 KB / 4213 字节）但解析出 0 个节点
    响应开头："<html><head><title>Just a moment...</title>..."   ← 是 HTML 页面（拦截页 / 挑战页 / 错误页），不是节点列表
⚠️  本次未解析出节点，3 秒后重试（源站抖动或返回了非节点内容）。
```

三行里就能判断问题出在哪：

| 诊断信息 | 说明 |
| :--- | :--- |
| `响应体为空（0 字节）` | 源站这次什么都没返回（边缘节点缓存了空文件、源站 200 空体）——**重试往往就好** |
| 开头是 `<`，提示「是 HTML 页面」 | 拿到了拦截页 / 挑战页 / 错误页。CF 的 `*.pages.dev` 在部分网络下会被拦截，也会出现这种情况 |
| 开头是 `{` / `[`，提示「是 JSON」 | 源改成了 JSON 且字段结构与解析规则不匹配，需要适配（见「数据源写法」） |
| 开头是正常节点列表却仍是 0 | 格式特殊（无端口且 `BARE_IP_DEFAULT_PORT=0`、非 IPv4、字段顺序异常等），把这段片段贴出来即可定位 |

同时，**「请求成功但解析出 0 个节点」现在会走重试流程**（与网络错误同等对待），因此源站一次抖动不会再让整条数据源白跑一轮。

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
│   ├── sieve.go               # 数据源 × 工序漏斗统计（筛子日志与汇总矩阵）
│   ├── util.go                # HTTP 客户端、并发与进度工具
│   ├── lock_unix.go           # 单实例锁（flock）
│   ├── lock_windows.go        # 单实例锁（Windows 独占句柄）
│   ├── parse_test.go          # 单元测试（解析引擎 / 多源去重）
│   └── sieve_test.go          # 单元测试（漏斗统计 / 标签缩写 / CJK 对齐）
├── deploy/                    # 部署相关
│   ├── app/                   # 运行时数据（挂载源，compose 中写作 ./app/...）
│   │   ├── config.json        # 配置文件模板（含逐项注释）
│   │   └── ip.txt             # 空白结果文件，程序运行时覆盖写入
│   ├── Dockerfile             # Alpine 多阶段构建（由 CI 使用，compose 不引用）
│   ├── docker-compose.yml     # 一键部署（从 GHCR 拉取镜像）
│   ├── docker-entrypoint.sh   # 容器入口（定时循环 / 参数透传）
│   └── .env.example           # 镜像名等环境变量模板
├── go.mod
├── README.md
└── .github/workflows/         # ci.yml：vet + test + 交叉编译 + 推送 GHCR
                               # release.yml：CI 全绿后自动发版
```

---

## 🔄 CI/CD

**`ci.yml`** —— 每次推送 / PR 自动执行：

1. `go vet` 静态检查
2. `go test` 单元测试（解析引擎、前置过滤、综合评分等）
3. `linux/amd64` 与 `linux/arm64` 交叉编译验证 + 二进制 `--version` 冒烟测试
4. `docker compose config` 插值校验（含挂载形态防回归断言）
5. 推送到 main 分支时：多架构构建镜像并推送 GHCR（PR 仅测试，不推送）

**`release.yml`** —— 由 CI 结果驱动，**不直接监听 push**：

```
push main ──> ci.yml ──> 完成（success）──> release.yml
                            │
                            └─ 失败 / PR / 带 [skip release] ──> 不发版
```

发布时依次完成：算日期版本号 → 建 tag 并推送 → 交叉编译 6 个平台（含 `config.json`）→ 产物自检 → 用该版本号重建 GHCR 镜像并自证镜像内版本 → 让 `latest` 指向它 → 创建 Release。

这样设计的原因是**不发拿不准的版本**：只有测试与镜像构建全绿才会产生 Release；镜像用已确定的版本号重建（而非给 CI 的 `sha-<短SHA>` 镜像补打标签）是为了让 `:<日期版本>` 与容器里 `cfnb --version` 的输出严格一致——补标签只改指向、不重新构建，镜像内版本会停留在 `sha-xxx`。两者参照同一个 `TARGET_SHA`，因此也没有竞态。

---

## ❓ 常见问题

1. **容器内跑完一次就退出了？** 镜像默认 `RUN_INTERVAL=0` 为单次运行；`docker compose` 默认 300 秒（5 分钟）循环，在 `deploy/.env` 里改 `RUN_INTERVAL` 即可。
2. **带宽测速全部失败？** 程序会降级使用 TCP 排序结果并发送微信通知；可适当调大 `BANDWIDTH_TIMEOUT`、降低 `BANDWIDTH_SIZE_MB`。
3. **TCP 测试无节点通过？** 这是第一道硬门槛（无回退）：检查网络能否直连，或降低 `MIN_SUCCESS_RATE`。
4. **DNS 更新记录数少于 `DNS_UPDATE_TARGET_COUNT`？** 属正常现象：端口 / IPv6 落地 / 黑名单 / 风险等级过滤会剔除部分节点，可通过增大 `BANDWIDTH_CANDIDATES` 扩大候选池（默认已设为 `300`）。日志里会分类列出被拦下的节点明细，照着看即可定位是哪条规则。
5. **最终优选不足 `GLOBAL_TOP_N` 个？** 主要来自四道筛减叠加：`📏 TCP 延迟过滤`（默认 90 ms）、`🛡 IPv6 落地过滤`（实测砍掉候选池的 59%~92%）、`🕐 HTTP 延迟过滤`（默认 180 ms）与 `📉 抖动过滤`（默认 30 ms）。**这四道默认全部开启**，日志里各有一行独立的 `N → M`，按行看筛减比例即可定位是谁削薄的：
   - 削薄最多的是 `🛡 IPv6 落地过滤`（属正常，可用 `BANDWIDTH_CANDIDATES` 调大候选池对冲）；
   - 若 `🕐 延迟过滤` 一刀砍掉九成以上，说明 180 ms 比该网络下的实测量级还严，放宽到 `600` 左右；
   - 若 `📉 抖动过滤` 同样砍掉八九成，放宽到 `100 ~ 200`；
   - 若 `📏 TCP 延迟过滤` 让候选池远小于 `BANDWIDTH_CANDIDATES`，放宽 `TCP_MAX_LATENCY_MS`（实测候选的 TCP 多在 65~93 ms，90 ms 已属偏严）。
   每一项都可以单独设成 `false` 关掉。
6. **开了 `🕐 延迟过滤` 后节点几乎被清空？** 阈值给得比实测量级还小。该延迟是「多次采样的最大值、完整 HTTP 往返」，本机实测普遍在 `180 ~ 3000 ms`（⚡TCP 才 `65 ~ 93 ms`），`180 ms` 属于很严的一档。先看那行的筛减比例与被拦节点的实际延迟值，再决定放宽到多少（例如 `600`）。
7. **开了 `📉 抖动过滤` 后节点只剩几个？** 同理，阈值偏严。抖动是单轮量、波动大，同一批节点换个时段结果可能差十几倍；先看日志 `📉 抖动过滤` 那行的筛减比例与明细里被拦节点的实际抖动值，再决定把 `PRE_BANDWIDTH_MAX_JITTER_MS` 放宽到多少。
8. **提示"检测到本程序已在运行"？** 单实例锁生效，避免定时任务重叠；锁文件为程序同目录 `.run.lock`。
9. **IP 地区校准很慢？** 调低 `IP_CALIBRATION_CONCURRENCY` 或增大 `IP_CALIBRATION_MIN_INTERVAL`；不使用则设 `IP_CALIBRATION_ENABLED: false`。
10. **代理环境影响？** 与 Python 版一致：TCP / HTTP / 测速阶段强制直连，API 类请求（抓取、可用性、通知、GitHub）跟随系统代理；`FORCE_DIRECT: true` 可全部直连。
11. **某个数据源明明有数据，却显示「解析出 0 个节点」？** 日志会紧接着打印 HTTP 状态、`Content-Type`、字节数和响应开头片段，照那段就能判断是空响应、HTML 拦截页还是格式不匹配（见[数据源解析出 0 个节点怎么办](#数据源解析出-0-个节点怎么办)）。程序也会把它当作抓取失败自动重试 `FETCH_MAX_RETRIES` 次，源站单次抖动不会再让整条源白跑一轮。
12. **日志里的进度条变成了十几行？** 这是刻意的：`docker logs` / 文件 / 日志查看器不是终端，`\r` 原地刷新会把几十帧挤成一行，所以检测到非终端时改为每 10% 打一行。想要回终端那样的单行刷新，用 `docker compose logs -f` 之外的方式（如 `docker attach`）或本地直接运行即可。

---

## 🙏 致谢

- 原项目（Python 版）：[xinyitang3/cfnb](https://github.com/xinyitang3/cfnb)
- 节点数据源 & 检测 API：[cmliussss](https://github.com/cmliussss)
- 高分 IP 列表：[yuanxiawan/cfipv4db](https://github.com/yuanxiawan/cfipv4db)
- 优选地址列表：[cmliu/WorkerVless2sub](https://github.com/cmliu/WorkerVless2sub)
- 社区优选域名：[cf.090227.xyz](https://cf.090227.xyz)（[090227.xyz](https://090227.xyz)）、[cf.877774.xyz](https://cf.877774.xyz)、[saas.sin.fan](https://saas.sin.fan)
- IP 风险检测 API：[ipapi.is](https://ipapi.is/)
- IP 地区校准：[ipinfo.io](https://ipinfo.io/)
- 微信通知服务：[WxPusher](https://wxpusher.zjiecode.com/)

---

**许可证**：本项目采用 [MIT License](https://opensource.org/licenses/MIT) 开源。
