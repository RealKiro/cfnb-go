package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// version 版本号。用 var 而非 const，以便发布时通过
// -ldflags "-X main.version=x.y.z" 注入 tag 对应的版本号。
var version = "1.0.0"

// globalForceDirect 强制直连开关（供工具层的客户端构造使用）
var globalForceDirect bool

// medals 最终优选列表的前三名标记（其余用序号）
var medals = []string{"🥇", "🥈", "🥉"}

func main() {
	// 便于 CI 与容器内快速校验二进制可用性
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version", "-v":
			fmt.Printf("cfnb-go %s\n", version)
			return
		case "--help", "-h":
			printUsage()
			return
		}
	}

	baseDir := exeDir()
	configPath := filepath.Join(baseDir, "config.json")
	// 允许通过环境变量指定配置文件位置（容器部署时可挂载到任意路径）
	if p := os.Getenv("CFNB_CONFIG"); p != "" {
		configPath = p
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		logf("错误：%v", err)
		os.Exit(1)
	}

	globalForceDirect = cfg.ForceDirect
	if cfg.ForceDirect {
		applyForceDirect()
	}

	// 单实例锁：已有实例在跑则直接退出
	lockPath := filepath.Join(baseDir, ".run.lock")
	if !acquireSingleInstance(lockPath) {
		logf("⚠️  检测到本程序已在运行，本次启动自动退出。")
		os.Exit(0)
	}
	defer releaseSingleInstance()

	run(&cfg, baseDir)
}

func run(cfg *Config, baseDir string) {
	notifier := NewNotifier(cfg)
	printBanner(cfg)

	// 筛子统计：每道工序后按数据源记录剩余节点数并打印，
	// run 结束时（含所有提前 return 的路径）输出「数据源 × 工序」汇总矩阵
	sieve := newSieveStats()
	defer sieve.printSummary()

	// ---------- 1. 聚合数据源 ----------
	nodes := fetchAllSources(cfg, sieve)
	sieve.recordNodes("去重合并", nodes)
	logf("🧮 合并后总计 %d 个节点。", len(nodes))

	// ---------- 2. IP 地区校准 ----------
	tokenFile := filepath.Join(baseDir, cfg.IPCalibrationTokenFile)
	cacheFile := filepath.Join(baseDir, cfg.IPCalibrationCacheFile)
	calibrateRegions(cfg, nodes, tokenFile, cacheFile, notifier)

	// ---------- 3. 前置过滤（按序：端口 → 黑名单 → 白名单）----------
	nodes = preFilter(cfg, nodes)
	sieve.recordNodes("前置过滤", nodes)
	if len(nodes) == 0 {
		logf("❌ 过滤后无任何有效节点，退出。")
		return
	}

	// ---------- 4. TCP 连接测试 ----------
	results := tcpTestAll(cfg, nodes)
	if len(results) == 0 {
		logf("❌ 没有通过成功率筛选的节点，请检查网络或降低 MIN_SUCCESS_RATE。")
		return
	}
	tcpPassed := make([]string, 0, len(results))
	for _, r := range results {
		tcpPassed = append(tcpPassed, r.Node)
	}
	sieve.recordNodes("TCP通过", tcpPassed)
	sortNodeResults(results)

	latencyMap := make(map[string]float64, len(results))
	for _, r := range results {
		latencyMap[r.Node] = r.Latency
	}

	countryNodes := groupByCountry(results)
	candidates := buildCandidates(cfg, results, countryNodes)
	sieve.recordNodes("候选池", candidates)
	if len(candidates) == 0 {
		logf("❌ 没有候选节点，退出。")
		return
	}

	// ---------- 5. 可用性 → IPv6 落地 → HTTP → 带宽 四级筛选 ----------
	candidates, availStacks := availabilityFilterWithRetry(cfg, candidates, notifier)
	sieve.recordNodes("可用通过", candidates)

	candidates = preBandwidthIPv6Filter(cfg, candidates, availStacks)
	if cfg.PreBandwidthIPv6FilterEnabled {
		sieve.recordNodes("IPv6过滤", candidates)
	}
	if len(candidates) == 0 {
		logf("❌ IPv6 落地过滤后已无剩余节点。如需保留仅 IPv6 可达的节点，把 PRE_BANDWIDTH_IPV6_FILTER_ENABLED 设为 false。")
		return
	}

	candidates, httpLatencyMap, httpJitterMap := httpServerFilter(cfg, candidates, notifier)
	sieve.recordNodes("HTTP通过", candidates)

	var bwResults []BandwidthResult
	for attempt := 1; attempt <= cfg.BandwidthRetryMax; attempt++ {
		logf("\n🚀 [带宽测速] 第 %d 轮测试...", attempt)
		bwResults = bandwidthFilter(cfg, candidates)
		if len(bwResults) > 0 {
			break
		}
		if attempt < cfg.BandwidthRetryMax {
			logf("本轮测速无有效结果，等待 %d 秒后重试...", cfg.BandwidthRetryDelay)
			sleepSeconds(cfg.BandwidthRetryDelay)
		}
	}
	bwPassed := make([]string, 0, len(bwResults))
	for _, r := range bwResults {
		bwPassed = append(bwPassed, r.Node)
	}
	sieve.recordNodes("带宽通过", bwPassed)

	// ---------- 6. 综合排序，产出最终节点 ----------
	speedMap := make(map[string]float64, len(bwResults))
	var finalSelected []string

	if len(bwResults) == 0 {
		logf("\n⚠️  带宽测速多次重试仍无有效结果，降级使用 TCP 筛选结果作为最终节点。")
		notifier.Send(
			fmt.Sprintf("带宽测速经 %d 轮尝试后仍无有效结果，已降级使用 TCP 排序节点。", cfg.BandwidthRetryMax),
			"带宽测速全部失败",
		)
		finalSelected = fallbackSelection(cfg, results, countryNodes)
	} else {
		for _, r := range bwResults {
			speedMap[r.Node] = r.Speed
		}
		finalSelected = scoreAndSelect(cfg, bwResults, latencyMap, httpLatencyMap, httpJitterMap)
	}
	sieve.recordNodes("最终入选", finalSelected)

	logf("\n============ 🏆 最终优选节点 ============")
	for i, node := range finalSelected {
		// 前三名用奖牌代替序号；其余保持「序号.」对齐
		prefix := fmt.Sprintf("%2d. ", i+1)
		if i < len(medals) {
			prefix = medals[i] + " "
		}
		line := prefix + node
		if v, ok := speedMap[node]; ok && v > 0 {
			line += fmt.Sprintf("   🚀 %.2f Mbps", v)
		}
		if v, ok := httpLatencyMap[node]; ok {
			line += fmt.Sprintf("   🌐 HTTP %.2f ms", v)
		}
		if v, ok := httpJitterMap[node]; ok {
			line += fmt.Sprintf("   📉 抖动 %.2f ms", v)
		}
		if v, ok := latencyMap[node]; ok {
			line += fmt.Sprintf("   ⚡ TCP %.2f ms", v*1000)
		}
		logf("%s", line)
	}

	// ---------- 7. 写出结果 ----------
	if err := writeIPTxt(cfg, finalSelected, speedMap, latencyMap, httpLatencyMap, httpJitterMap); err != nil {
		logf("写入 %s 失败: %v", cfg.OutputFile, err)
		return
	}
	logf("\n💾 结果已保存到 %s（共 %d 个节点）", cfg.OutputFile, len(finalSelected))

	// ---------- 8. Cloudflare DNS 更新 ----------
	ipList := make([]string, 0, len(finalSelected))
	for _, node := range finalSelected {
		ip, _, _ := strings.Cut(node, ":")
		ipList = append(ipList, ip)
	}
	dnsWritten := batchUpdateCloudflareDNS(cfg, notifier, ipList, availStacks, bwResults, latencyMap, httpLatencyMap, httpJitterMap)
	// 仅在启用 CF 更新时记录该工序：未启用则整道工序没跑，记 0 会误导
	if cfg.CFEnabled {
		sieve.recordNodes("DNS写入", dnsWritten)
	}

	// ---------- 9. GitHub 同步 ----------
	syncToGitHub(cfg, notifier)
}

// printBanner 打印运行参数摘要
func printBanner(cfg *Config) {
	logf(strings.Repeat("=", 62))
	logf(" ☁️  cfnb-go %s   Cloudflare 优选节点自动筛选", version)
	logf(strings.Repeat("=", 62))

	mode := fmt.Sprintf("全局最优%d个", cfg.GlobalTopN)
	if !cfg.UseGlobalMode {
		mode = fmt.Sprintf("每个国家最优%d个", cfg.PerCountryTopN)
	}
	logf("🎯 当前模式：%s，每个节点测试 %d 次 TCP 连接", mode, cfg.TCPProbes)
	logf("📉 最低成功率要求：%.0f%%", cfg.MinSuccessRate*100)
	logf("🩺 IP 可用性二次筛选：%s（仅对候选节点）", enabledText(cfg.TestAvailability))
	logf("🌐 HTTP检测：%s（仅对候选节点）", enabledText(cfg.HTTPTestEnabled))
	logf("🛡️  IPv6 落地过滤：测速前 %s ｜ DNS 写入前 %s（剔除仅 IPv6 可达的节点）",
		enabledText(cfg.PreBandwidthIPv6FilterEnabled), enabledText(cfg.FilterIPv6Availability))
	logf("🚫 DNS黑名单过滤：%s，黑名单国家：%s", enabledText(cfg.FilterBlockedCountriesEnabled), strings.Join(cfg.BlockedCountries, ", "))
	if cfg.PreFilterBlockedEnabled {
		src := "仅 PRE_FILTER_BLOCKED_COUNTRIES"
		if cfg.PreFilterUseDNSBlocklist {
			src = "已并入 DNS 黑名单"
		}
		logf("🚧 前置黑名单过滤：启用，共 %d 国（%s）", len(preFilterBlockedCountries(cfg)), src)
	}
	logf("☣️  IP 风险等级过滤：%s（最高允许：%s）", enabledText(cfg.DNSIPRiskFilterEnabled), cfg.DNSIPRiskMaxLevel)
	logf("🚀 带宽测速候选数：%d，测速文件大小：%.1f MB，超时：%ds", cfg.BandwidthCandidat, cfg.BandwidthSizeMB, cfg.BandwidthTimeout)
	if cfg.FilterCountriesEnabled {
		logf("✅ 前置白名单过滤：启用，仅保留：%s", strings.Join(cfg.AllowedCountries, ", "))
	}
}

func enabledText(b bool) string {
	if b {
		return "启用"
	}
	return "禁用"
}

func printUsage() {
	fmt.Printf(`cfnb-go %s — Cloudflare CDN 节点优选工具（Go 版）

用法:
  cfnb                 读取同目录 config.json 并执行一次完整优选流程
  cfnb --version       打印版本号
  cfnb --help          打印本帮助

配置:
  所有参数位于程序同目录的 config.json，字段与 Python 版完全兼容，
  另新增 GITHUB_* 字段用于通过 GitHub Contents API 同步 ip.txt。

环境变量:
  RUN_INTERVAL  由容器 entrypoint 使用，循环运行间隔（秒），0=只运行一次
  TZ            时区，例如 Asia/Shanghai

输出:
  ip.txt         优选结果（每行 IP:端口#国家码）
  ipinfo_cache.txt  IP 地区校准缓存（启用校准时生成）
`, version)
}

// fetchAllSources 抓取所有启用的数据源并按 ip:port 去重合并。
// 归属统计与去重同口径：同一 ip:port 出现在多个源时记给第一个贡献它的源。
func fetchAllSources(cfg *Config, sieve *sieveStats) []string {
	client := newHTTPClient(
		time.Duration(cfg.FetchConnectTimout)*time.Second,
		time.Duration(cfg.FetchTimeout)*time.Second,
		true,
	)

	var nodes []string
	seen := map[string]struct{}{}
	rawCounts := map[string]int{}
	for _, source := range cfg.AdditionalSources {
		if !source.IsEnabled() || source.URL == "" {
			continue
		}
		label := sieve.registerSource(source.URL)
		fetched := fetchAdditionalSource(cfg, client, source.URL)
		rawCounts[label] += len(fetched)
		for _, node := range fetched {
			key := nodeKeyOf(node)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			sieve.claim(key, label)
			nodes = append(nodes, node)
		}
	}
	// 第一道工序：各源抓取到的原始节点数（去重前，含被其他源抢先的重复节点）
	sieve.recordCounts("抓取", rawCounts)
	return nodes
}

// preFilter 前置过滤：端口 → 黑名单 → 白名单（均在 TCP 测试前执行）
func preFilter(cfg *Config, nodes []string) []string {
	if cfg.PreFilterPortEnabled && len(cfg.PreFilterPorts) > 0 {
		before := len(nodes)
		allowedPorts := map[string]struct{}{}
		for _, p := range cfg.PreFilterPorts {
			allowedPorts[strconv.Itoa(p)] = struct{}{}
		}
		filtered := nodes[:0:0]
		for _, node := range nodes {
			_, rest, ok := strings.Cut(node, ":")
			if !ok {
				continue
			}
			port, _, _ := strings.Cut(rest, "#")
			if _, allowed := allowedPorts[port]; allowed {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
		portsDisplay := make([]string, 0, len(cfg.PreFilterPorts))
		for _, p := range cfg.PreFilterPorts {
			portsDisplay = append(portsDisplay, strconv.Itoa(p))
		}
		logf("🚧 前置端口过滤（仅保留端口 %s）：%d -> %d 个节点", strings.Join(portsDisplay, ", "), before, len(nodes))
		if len(nodes) == 0 {
			return nil
		}
	}

	if blockedCountries := preFilterBlockedCountries(cfg); cfg.PreFilterBlockedEnabled && len(blockedCountries) > 0 {
		before := len(nodes)
		blocked := map[string]struct{}{}
		for _, c := range blockedCountries {
			blocked[c] = struct{}{}
		}
		filtered := nodes[:0:0]
		for _, node := range nodes {
			// 无国家标签的节点（CF 官方 anycast IP）不参与国家黑名单判断：
			// 拿不到落地国家就宁可放过，也不误杀（与 DNS 阶段的口径一致）
			if _, isBlocked := blocked[strings.ToUpper(firstField(nodeTag(node)))]; !isBlocked {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
		logf("🚧 前置黑名单过滤：%d -> %d 个节点（已屏蔽：%s）", before, len(nodes), strings.Join(blockedCountries, ", "))
		if len(nodes) == 0 {
			return nil
		}
	}

	if cfg.FilterCountriesEnabled && len(cfg.AllowedCountries) > 0 {
		before := len(nodes)
		allowed := map[string]struct{}{}
		for _, c := range cfg.AllowedCountries {
			allowed[strings.ToUpper(c)] = struct{}{}
		}
		filtered := nodes[:0:0]
		for _, node := range nodes {
			if !strings.Contains(node, "#") {
				continue
			}
			if _, ok := allowed[strings.ToUpper(firstField(nodeTag(node)))]; ok {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
		allowedDisplay := make([]string, 0, len(allowed))
		for c := range allowed {
			allowedDisplay = append(allowedDisplay, c)
		}
		logf("\n🚧 国家过滤（测试前）：%d -> %d 个节点（允许国家：%s）", before, len(nodes), strings.Join(sortStrings(allowedDisplay), ", "))
	}
	return nodes
}

// preFilterBlockedCountries 返回前置黑名单实际生效的国家（已去重、排序）。
//
// PRE_FILTER_USE_DNS_BLOCKLIST 开启时并入 DNS 环节的 BLOCKED_COUNTRIES：
// 两道闸名单若不一致，只会被 DNS 拦下的国家，其节点仍要走完 TCP/HTTP/带宽
// 三段才在后段被淘汰——实测 HK 节点就这样白跑了一整轮（而带宽测速占整轮
// 耗时的一半）。默认对齐以消除这种浪费。
func preFilterBlockedCountries(cfg *Config) []string {
	if !cfg.PreFilterUseDNSBlocklist {
		return normalizeCountries(cfg.PreFilterBlockedCountries)
	}
	merged := make([]string, 0, len(cfg.PreFilterBlockedCountries)+len(cfg.BlockedCountries))
	merged = append(merged, cfg.PreFilterBlockedCountries...)
	merged = append(merged, cfg.BlockedCountries...)
	return normalizeCountries(merged)
}

// normalizeCountries 国家码规整：去空白、转大写、去重、排序
func normalizeCountries(items []string) []string {
	set := map[string]struct{}{}
	for _, c := range items {
		if c = strings.ToUpper(strings.TrimSpace(c)); c != "" {
			set[c] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	return sortStrings(out)
}

// preBandwidthIPv6Filter 在 HTTP 检测与带宽测速之前剔除「仅 IPv6 可达」的节点。
//
// 为什么放在这里而不是只留在 DNS 环节：带宽测速是整轮最耗时的一段（实测约
// 占一半），而 ipv6_only 节点在 DNS 写入前必定被剔除——晚过滤等于白测。
// 提前过滤后 ip.txt 与写入 DNS 的名单也会收敛，不再出现「两套互不相干的
// 结果」（含 9/10 为 ipv6_only 的 HK 节点那种情形）。
//
// 数据来自可用性检测返回的 inferred_stack。未开启可用性检测、或该轮可用性
// 检测整体失败时拿不到协议栈信息，此处自然退化为空操作（不会误杀）。
func preBandwidthIPv6Filter(cfg *Config, candidates []string, stacks map[string]string) []string {
	if !cfg.PreBandwidthIPv6FilterEnabled || len(candidates) == 0 {
		return candidates
	}
	if len(stacks) == 0 {
		logf("ℹ️  未取得节点协议栈信息（可用性检测未启用或本轮失败），本轮跳过 IPv6 落地过滤。")
		return candidates
	}

	filtered := make([]string, 0, len(candidates))
	dropped := 0
	for _, node := range candidates {
		if stacks[node] == "ipv6_only" {
			dropped++
			continue
		}
		filtered = append(filtered, node)
	}
	logf("🛡️  IPv6 落地过滤（测速前）：%d -> %d 个节点（剔除仅 IPv6 可达 %d 个）",
		len(candidates), len(filtered), dropped)
	return filtered
}

// nodeTag 取节点 # 后的标签
func nodeTag(node string) string {
	idx := strings.LastIndex(node, "#")
	if idx < 0 {
		return ""
	}
	return node[idx+1:]
}

// tcpTestAll 并发完成 TCP 测试
func tcpTestAll(cfg *Config, nodes []string) []*NodeResult {
	logf("🔌 开始 TCP 连接测试（超时 %.1fs，并发 %d）...", cfg.Timeout, cfg.MaxWorkers)
	pp := newProgressPrinter(cfg.ProgressPrintInterval, "🔌 [TCP测试]")

	results, _ := parallelRunProgress(nodes, cfg.MaxWorkers,
		func(node string) (*NodeResult, bool) {
			r := testNode(cfg, node)
			return r, r != nil
		}, pp, "")

	logf("✅ TCP 测试完成！")
	return results
}

// groupByCountry 按国家分组（保持稳定顺序）
func groupByCountry(results []*NodeResult) map[string][]*NodeResult {
	grouped := map[string][]*NodeResult{}
	for _, r := range results {
		grouped[r.Country] = append(grouped[r.Country], r)
	}
	return grouped
}

// buildCandidates 构建进入测速的候选池
func buildCandidates(cfg *Config, results []*NodeResult, countryNodes map[string][]*NodeResult) []string {
	if cfg.UseGlobalMode {
		limit := cfg.BandwidthCandidat
		if limit > len(results) {
			limit = len(results)
		}
		candidates := make([]string, 0, limit)
		for _, r := range results[:limit] {
			candidates = append(candidates, r.Node)
		}
		logf("\n🎯 TCP 最优前 %d 个节点进入候选池。", len(candidates))
		return candidates
	}

	totalCountries := len(countryNodes)
	if totalCountries == 0 {
		return nil
	}
	baseLimit := cfg.BandwidthCandidat / totalCountries
	if baseLimit < 1 {
		baseLimit = 1
	}

	var candidates []string
	for _, country := range sortedCountryKeys(countryNodes) {
		items := countryNodes[country]
		sortNodeResults(items)
		limit := len(items)
		if limit > baseLimit {
			limit = baseLimit
		}
		for _, r := range items[:limit] {
			candidates = append(candidates, r.Node)
		}
	}
	logf("\n🎯 各国家候选池分配：共 %d 个国家，每国最多 %d 个候选，总计 %d 个节点进入候选池。", totalCountries, baseLimit, len(candidates))
	return candidates
}

func sortedCountryKeys(grouped map[string][]*NodeResult) []string {
	keys := make([]string, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	return sortStrings(keys)
}

// scoredNode 综合评分结果
type scoredNode struct {
	Node  string
	Score float64
}

// scoreAndSelect 综合加权排序并选出最终节点
func scoreAndSelect(
	cfg *Config,
	bwResults []BandwidthResult,
	latencyMap, httpLatencyMap, httpJitterMap map[string]float64,
) []string {
	scored := make([]scoredNode, 0, len(bwResults))
	for _, r := range bwResults {
		tcpLat, ok := latencyMap[r.Node]
		if !ok {
			tcpLat = 999.0
		}
		httpLat, ok := httpLatencyMap[r.Node]
		if !ok {
			httpLat = 999999.0
		}
		jitter, ok := httpJitterMap[r.Node]
		if !ok {
			jitter = 999999.0
		}

		penalty := 1.0 +
			cfg.TCPLatencyWeight*tcpLat +
			cfg.HTTPLatencyWeight*httpLat/1000.0 +
			cfg.JitterWeight*jitter/1000.0
		scored = append(scored, scoredNode{Node: r.Node, Score: cfg.SpeedWeight * r.Speed / penalty})
	}

	// 得分降序（稳定排序，保持同分时速度降序的相对顺序）
	for i := 1; i < len(scored); i++ {
		for j := i; j > 0 && scored[j].Score > scored[j-1].Score; j-- {
			scored[j], scored[j-1] = scored[j-1], scored[j]
		}
	}

	if cfg.UseGlobalMode {
		limit := cfg.GlobalTopN
		if limit > len(scored) {
			limit = len(scored)
		}
		out := make([]string, 0, limit)
		for _, s := range scored[:limit] {
			out = append(out, s.Node)
		}
		return out
	}

	// 分国家模式：每国取前 N，再按得分整体排序
	countryScored := map[string][]scoredNode{}
	for _, s := range scored {
		country := nodeTag(s.Node)
		if country == "" {
			continue
		}
		countryScored[country] = append(countryScored[country], s)
	}

	scoreDict := make(map[string]float64, len(scored))
	for _, s := range scored {
		scoreDict[s.Node] = s.Score
	}

	var selected []string
	for _, country := range sortedScoredKeys(countryScored) {
		items := countryScored[country]
		limit := cfg.PerCountryTopN
		if limit > len(items) {
			limit = len(items)
		}
		for _, s := range items[:limit] {
			selected = append(selected, s.Node)
		}
	}

	// 按得分降序
	for i := 1; i < len(selected); i++ {
		for j := i; j > 0 && scoreDict[selected[j]] > scoreDict[selected[j-1]]; j-- {
			selected[j], selected[j-1] = selected[j-1], selected[j]
		}
	}
	return selected
}

func sortedScoredKeys(grouped map[string][]scoredNode) []string {
	keys := make([]string, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	return sortStrings(keys)
}

// fallbackSelection 带宽测速全部失败时，降级使用 TCP 结果
func fallbackSelection(cfg *Config, results []*NodeResult, countryNodes map[string][]*NodeResult) []string {
	var selected []string
	if cfg.UseGlobalMode {
		limit := cfg.GlobalTopN
		if limit > len(results) {
			limit = len(results)
		}
		for _, r := range results[:limit] {
			selected = append(selected, r.Node)
		}
		return selected
	}

	for _, country := range sortedCountryKeys(countryNodes) {
		items := countryNodes[country]
		sortNodeResults(items)
		limit := cfg.PerCountryTopN
		if limit > len(items) {
			limit = len(items)
		}
		for _, r := range items[:limit] {
			selected = append(selected, r.Node)
		}
	}
	return selected
}
