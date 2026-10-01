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
| 🧩 **多源自适应聚合** | 支持任意格式（标准代码 / 中文名 / emoji 国旗 / JSON），裸 IP 自动补默认端口，`IP # 标签` 这类带空格的写法也能识别；源可直接写 URL，也可写 **域名**（自动 DNS 解析取 A 记录）；多源合并按 `ip:port` 去重，靠前的源优先 |
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

每个压缩包内含 **二进制 + `config.json`（带逐项注释的配置模板）**，解压后两者同目录，程序会自动读取：

```bash
tar -xzf cfnb-2026.10.01-linux-amd64.tar.gz
# 解压得到：cfnb-2026.10.01-linux-amd64（二进制）、config.json（配置模板）
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
- **版本号**一律用日期标签 `vYYYY.MM.DD`（东八区），不递增语义化版本号。同一天多次发布自动追加序号：`v2026.10.01` → `v2026.10.01.2` → `v2026.10.01.3`
- 整个发版动作无需人工介入：算版本号 → 建 tag → 交叉编译 6 个平台 → 把 `config.json` 一并打包 → 创建 Release（含 `checksums.txt`）→ 给 `sha-<短SHA>` 镜像补打同一版本标签
- **只想跑 CI、不发版**：让提交标题以 `[skip release]` 开头即可（锚定标题，写在正文里不影响）。只改 `README.md` / `LICENSE` 的提交本来就不会触发 CI，因此也不会发版
- 需要补发历史版本或指定版本号时，仍可手动打标签（走同一套流程）：

```bash
git tag v2026.10.02 && git push origin v2026.10.02
```

镜像标签与 Release 一一对应：`latest` 跟随 `main` 最新一次成功发版，版本标签形如 `ghcr.io/realkiro/cfnb-go:2026.10.02`。

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
| 镜像地址 | 默认 `ghcr.io/realkiro/cfnb-go:latest`（另有日期标签如 `2026.10.01` 与 `sha-xxxxxxx` 精确提交标签） |
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
3. **单轮耗时是间隔之外的**：`RUN_INTERVAL` 是**跑完一轮后 sleep 的时长**，真实周期 = 单轮耗时 + 间隔。完整一轮要抓全部数据源（默认 7 个源、去重后约 1.6 万条）并依次跑 TCP / HTTP / 带宽测速，本身就要几十秒到几分钟。
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
| `ADDITIONAL_SOURCES` | 7 个源 | 节点数据源列表，每项 `{ "url": ..., "enabled": true }`。**多源结果按 `ip:port` 自动去重**，保留先出现的节点，因此靠前的源优先级更高。支持两种写法，见下方《数据源写法》 |
| `BARE_IP_DEFAULT_PORT` | `443` | 数据源只返回裸 IP（无端口）时补的端口；`0` = 不补并丢弃这类节点。`ipdb.api.030101.xyz`、域名直填源等都依赖此项 |
| `KEEP_UNLABELED_NODES` | `true` | 是否保留「无国家标签」的节点。见下方《为什么必须开启 `KEEP_UNLABELED_NODES`》 |

### 数据源写法

`ADDITIONAL_SOURCES` 里每项按**写法**自适应，不需要额外的类型字段：

| 写法 | 处理方式 | 例子 |
| :--- | :--- | :--- |
| 以 `http://` / `https://` 开头 | 按 URL 拉取，自适应解析纯文本 / JSON（标准代码、中文名、emoji 国旗均可） | `"https://zip.cm.edu.kg/all.txt"` |
| 其余（域名 / 裸 IP） | **直填**：对该域名做 DNS 解析，取其**全部 A 记录**当候选；IP 形式则原样使用 | `"cf.090227.xyz"`、`"cmcc.090227.xyz:8443"`、`"1.2.3.4"` |

直填源每次解析都可能得到不同的一批地址（社区优选域名背后是维护者动态更新的 IP），端口取源内自带端口或 `BARE_IP_DEFAULT_PORT`。

当前默认的 7 个源：

| 源 | 类型 | 说明 |
| :--- | :--- | :--- |
| `zip.cm.edu.kg/all.txt` | URL | 综合大列表（约 1.5 万条），带国家标签 |
| `countrymerge.pages.dev/all.txt` | URL | 综合列表（约 1.6 千条），带国家标签 |
| `ipdb.api.030101.xyz?type=bestproxy` | URL | 第三方反代 IP（`type=bestcf` 则为 CF 官方 IP） |
| `yuanxiawan/cfipv4db` | URL | 韩国 VPS 扫描的高分 IP，更新频繁，**全是 CF 官方 anycast IP** |
| `cmliu/WorkerVless2sub` | URL | 整理过的优选地址列表，带国家标签 |
| `cf.090227.xyz` | 域名直填 | 老牌优选域名，三网自适应 |
| `cmcc.090227.xyz` | 域名直填 | 同上，移动线路专门优化 |

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

发布时依次完成：算日期版本号 → 建 tag 并推送 → 交叉编译 6 个平台（含 `config.json`）→ 产物自检 → 补打 GHCR 版本镜像标签 → 创建 Release。

这样设计的原因是**不发拿不准的版本**：只有测试与镜像构建全绿才会产生 Release；同时 CI 已经把 `sha-<短SHA>` 镜像推上去了，补打版本标签时无需重新构建、也没有竞态。

---

## ❓ 常见问题

1. **容器内跑完一次就退出了？** 镜像默认 `RUN_INTERVAL=0` 为单次运行；`docker compose` 默认 300 秒（5 分钟）循环，在 `deploy/.env` 里改 `RUN_INTERVAL` 即可。
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
- 高分 IP 列表：[yuanxiawan/cfipv4db](https://github.com/yuanxiawan/cfipv4db)
- 优选地址列表：[cmliu/WorkerVless2sub](https://github.com/cmliu/WorkerVless2sub)
- 社区优选域名：`cf.090227.xyz` / `cmcc.090227.xyz`（[090227.xyz](https://090227.xyz)）
- IP 风险检测 API：[ipapi.is](https://ipapi.is/)
- IP 地区校准：[ipinfo.io](https://ipinfo.io/)
- 微信通知服务：[WxPusher](https://wxpusher.zjiecode.com/)

---

**许可证**：本项目采用 [MIT License](https://opensource.org/licenses/MIT) 开源。
