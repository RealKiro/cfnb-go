package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ==================== 自适应多数据源解析引擎 ====================

// trimLeadingNoise 去掉 token 开头的数字/空白/连字符等噪声，对应 Python 的 ^[\d\s\-_.|#]+
func trimLeadingNoise(s string) string {
	i := 0
	for i < len(s) {
		c := rune(s[i])
		if unicode.IsDigit(c) || unicode.IsSpace(c) || c == '-' || c == '_' || c == '.' || c == '|' || c == '#' {
			i++
			continue
		}
		break
	}
	return s[i:]
}

func isUpperASCII(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLetterASCII(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

// extractCountryCode 从任意标签中提取标准两位国家代码
// 支持：两位代码、三位代码映射、中文名、emoji 国旗、混合无关文字
func extractCountryCode(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}

	tokens := splitLabelTokens(label)

	// 1. 两位 / 三位代码（先三位后两位，且后一位不能是字母）
	for _, token := range tokens {
		cleaned := trimLeadingNoise(strings.TrimSpace(token))
		if len(cleaned) >= 3 && isUpperASCII(cleaned[0]) && isUpperASCII(cleaned[1]) && isUpperASCII(cleaned[2]) {
			if len(cleaned) == 3 || !isLetterASCII(cleaned[3]) {
				if code, ok := alpha3ToAlpha2[cleaned[:3]]; ok {
					return code
				}
			}
		}
		if len(cleaned) >= 2 && isUpperASCII(cleaned[0]) && isUpperASCII(cleaned[1]) {
			if len(cleaned) == 2 || !isLetterASCII(cleaned[2]) {
				if isValidCode(cleaned[:2]) {
					return cleaned[:2]
				}
			}
		}
	}

	// 2. 中文子串提取
	for _, token := range tokens {
		cleaned := trimLeadingNoise(token)
		for _, run := range cjkRuns(cleaned) {
			if code, ok := cnToCode[run]; ok {
				return code
			}
		}
	}

	// 3. 国旗 emoji（区域指示符号 U+1F1E6..U+1F1FF）
	var flags []rune
	for _, r := range label {
		if r >= 0x1F1E6 && r <= 0x1F1FF {
			flags = append(flags, r)
		}
	}
	if len(flags) >= 2 && len(flags)%2 == 0 {
		first := flags[0] - 0x1F1E6
		second := flags[1] - 0x1F1E6
		if first >= 0 && first <= 25 && second >= 0 && second <= 25 {
			return string(rune('A'+first)) + string(rune('A'+second))
		}
	}

	return ""
}

// splitLabelTokens 按空白 , ; | / 切分标签
func splitLabelTokens(label string) []string {
	return strings.FieldsFunc(label, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\r', ',', ';', '|', '/':
			return true
		}
		return false
	})
}

// cjkRuns 提取连续的中文/括号片段，对应 Python 的 [\u4e00-\u9fff（）()]+
func cjkRuns(s string) []string {
	var runs []string
	var cur strings.Builder
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || r == '（' || r == '）' || r == '(' || r == ')' {
			cur.WriteRune(r)
		} else if cur.Len() > 0 {
			runs = append(runs, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		runs = append(runs, cur.String())
	}
	return runs
}

// isIPPort 校验 ip:port 形式（仅 IPv4）
func isIPPort(s string) bool {
	host, port, ok := strings.Cut(s, ":")
	if !ok || port == "" {
		return false
	}
	if strings.HasPrefix(host, "[") {
		return false
	}
	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	if len(port) > 5 {
		return false
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

// isIPv4 校验纯 IPv4 字面量（不含端口、不含冒号）
func isIPv4(s string) bool {
	if strings.ContainsAny(s, ":#") {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	for _, p := range parts {
		if p == "" || len(p) > 3 {
			return false
		}
		for i := 0; i < len(p); i++ {
			if p[i] < '0' || p[i] > '9' {
				return false
			}
		}
	}
	return true
}

// withDefaultPort 把节点规整成 ip:port：
//   - 已带端口：原样返回
//   - 纯 IPv4（无端口）：按 cfg.BareIPDefaultPort 补端口，端口 <=0 表示不补、丢弃
//   - 其余（IPv6、域名、非法值）：丢弃
//
// 有些优选 API（如 ipdb.api.030101.xyz）只吐裸 IP，不带端口，
// 而本项目后续所有环节（端口前置过滤、TCP 拨号、HTTP 检测）都要求 ip:port。
func withDefaultPort(cfg *Config, s string) (string, bool) {
	if isIPPort(s) {
		return s, true
	}
	if cfg.BareIPDefaultPort > 0 && isIPv4(s) {
		return s + ":" + strconv.Itoa(cfg.BareIPDefaultPort), true
	}
	return "", false
}

// labelHashRe 匹配「空白 + # + 空白」形式的标签分隔符。
// 部分数据源把国家标签写成 "104.24.76.225 # US-SJC"（如 yuanxiawan/cfipv4db
// 的 high_score_ips.txt）。直接按空白切分会让 "#" 与标签各自成 token，
// 于是 IP 变成无标签节点、标签被丢弃。这里先规整成 ip#label 再切分。
var labelHashRe = regexp.MustCompile(`\s*#\s*`)

// parseTextNodes 解析纯文本节点列表
func parseTextNodes(cfg *Config, text string) []string {
	text = labelHashRe.ReplaceAllString(text, "#")

	var nodes []string
	var pending []string

	for _, token := range strings.Fields(text) {
		ipport, label, _ := strings.Cut(token, "#")
		ipport = strings.TrimSpace(ipport)
		label = strings.TrimSpace(label)

		if strings.HasPrefix(ipport, "[") {
			continue
		}
		// 统一规整成 ip:port —— 裸 IP（无端口）在此按 BARE_IP_DEFAULT_PORT 补端口，
		// 兼容那些只吐 IP 不给端口的优选 API，例如 ipdb.api.030101.xyz。
		ipport, ok := withDefaultPort(cfg, ipport)
		if !ok {
			continue
		}

		if code := extractCountryCode(label); code != "" {
			nodes = append(nodes, ipport+"#"+code)
		} else {
			pending = append(pending, ipport)
		}
	}

	if len(pending) > 0 {
		if cfg.KeepUnlabeledNodes {
			// 保留无国家标签的节点。典型来源是 Cloudflare 官方 anycast IP 源
			// （如 yuanxiawan/cfipv4db、社区优选域名），它们本就不提供落地国家，
			// 而可用性 API 的语义是「该 IP 能否作为反代」，对 CF 官方 IP 恒为
			// success=false——查询既浪费时间又会把节点全部丢弃。
			// 这类节点改由后续 HTTP 检测（/cdn-cgi/trace 要求返回 400 且
			// Server 头为 cloudflare）把关，它才是「是不是 CF 边缘」的实证。
			logf("%d 个节点无国家标签，按 KEEP_UNLABELED_NODES=true 直接保留（跳过国家查询）", len(pending))
			nodes = append(nodes, pending...)
		} else {
			logf("%d 个节点未能识别或缺少国家，通过可用性检测 API 查询国家...", len(pending))
			resolved := resolveCountriesBatch(cfg, pending)
			for _, ipport := range pending {
				if code, ok := resolved[ipport]; ok && code != "" {
					nodes = append(nodes, ipport+"#"+code)
				}
			}
		}
	}

	return nodes
}

// ==================== 域名 / 裸 IP 直填源 ====================

// isDirectSource 判断数据源是否应作为「地址直填」处理（而非 HTTP 拉取）。
// 约定：以 http:// 或 https:// 开头的走 HTTP 拉取，其余视为域名或裸 IP。
// 这样社区优选域名（cf.090227.xyz）可以直接写进 ADDITIONAL_SOURCES，
// 无需额外的 type 字段——程序按写法自适应。
func isDirectSource(raw string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return false
	}
	if strings.ContainsAny(s, "/ \t?#") {
		return false
	}
	host, _, _ := strings.Cut(s, ":")
	if host == "" {
		return false
	}
	return isIPv4(host) || strings.Contains(host, ".")
}

// resolveDirectSource 把「域名或裸 IP」直填源展开成节点列表。
// 域名会查询全部 A 记录（仅取 IPv4），端口取源内自带端口或 BARE_IP_DEFAULT_PORT。
// 这类源（如社区优选域名 cf.090227.xyz / cmcc.090227.xyz）背后是维护者
// 动态更新的优选 IP，每次解析都可能得到不同的一批地址。
func resolveDirectSource(cfg *Config, spec string) []string {
	host, portStr, hasPort := strings.Cut(spec, ":")
	host = strings.TrimSpace(host)

	port := cfg.BareIPDefaultPort
	if hasPort && portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil && p > 0 && p <= 65535 {
			port = p
		}
	}
	if port <= 0 {
		logf("数据源 %s 未能确定端口（BARE_IP_DEFAULT_PORT=%d），跳过。", spec, cfg.BareIPDefaultPort)
		return nil
	}
	portS := strconv.Itoa(port)

	if isIPv4(host) {
		return []string{host + ":" + portS}
	}

	addrs, err := net.LookupHost(host)
	if err != nil {
		logf("DNS 解析 %s 失败: %v", host, err)
		return nil
	}

	var nodes []string
	seen := map[string]struct{}{}
	for _, a := range addrs {
		if !isIPv4(a) {
			continue // 只取 IPv4：后续 TCP 拨号与 HTTP 检测均基于 IPv4
		}
		node := a + ":" + portS
		if _, dup := seen[node]; dup {
			continue
		}
		seen[node] = struct{}{}
		nodes = append(nodes, node)
	}
	return nodes
}

// parseJSONNodes 递归解析 JSON 结构中的节点
func parseJSONNodes(cfg *Config, data any) []string {
	var nodes []string
	switch v := data.(type) {
	case []any:
		for _, item := range v {
			nodes = append(nodes, parseJSONNodes(cfg, item)...)
		}
	case map[string]any:
		for _, key := range []string{"nodes", "data", "result", "list"} {
			if arr, ok := v[key].([]any); ok {
				nodes = append(nodes, parseJSONNodes(cfg, arr)...)
				break
			}
		}
		ip := firstString(v, "ip", "host")
		code := firstString(v, "country", "cc")
		if ip != "" && code != "" {
			if port, ok := toPortString(v["port"]); ok {
				nodes = append(nodes, fmt.Sprintf("%s:%s#%s", ip, port, strings.ToUpper(code)))
			}
		}
	case string:
		nodes = append(nodes, parseTextNodes(cfg, v)...)
	}
	return nodes
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func toPortString(v any) (string, bool) {
	switch p := v.(type) {
	case string:
		if p != "" {
			return p, true
		}
	case float64:
		return fmt.Sprintf("%.0f", p), true
	case json.Number:
		return p.String(), true
	}
	return "", false
}

// parseAdaptive 自适应解析：自动识别 JSON 或文本格式
func parseAdaptive(cfg *Config, text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		var data any
		if err := json.Unmarshal([]byte(text), &data); err == nil {
			return parseJSONNodes(cfg, data)
		}
	}
	return parseTextNodes(cfg, text)
}

// queryCountry 通过可用性 API 查询单个节点的落地国家
func queryCountry(cfg *Config, client *http.Client, ip, port string) string {
	endpoint := fmt.Sprintf("%s?proxyip=%s", cfg.AvailabilityCheckAPI, url.QueryEscape(ip+":"+port))
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var data struct {
		ProbeResults map[string]struct {
			Exit struct {
				Country string `json:"country"`
			} `json:"exit"`
		} `json:"probe_results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return ""
	}
	if v4, ok := data.ProbeResults["ipv4"]; ok && len(v4.Exit.Country) == 2 {
		return strings.ToUpper(v4.Exit.Country)
	}
	return ""
}

// resolveCountriesBatch 并发批量查询国家（对应 Python 的 _resolve_countries_batch）
func resolveCountriesBatch(cfg *Config, ipports []string) map[string]string {
	results := make(map[string]string, len(ipports))
	if len(ipports) == 0 {
		return results
	}

	client := newHTTPClient(
		time.Duration(cfg.AvailabilityConnectTimout)*time.Second,
		time.Duration(cfg.AvailabilityTimeout)*time.Second,
		true,
	)

	pp := newProgressPrinter(cfg.ProgressPrintInterval, "🧭 [备用API查询]")
	workers := cfg.FallbackWorkers
	if workers < 1 {
		workers = 1
	}

	type kv struct {
		key string
		val string
	}
	sem := make(chan struct{}, workers)
	ch := make(chan kv, len(ipports))
	var wg doneGroup

	for _, ipp := range ipports {
		wg.Add(1)
		sem <- struct{}{}
		go func(target string) {
			defer wg.Done()
			defer func() { <-sem }()
			host, port, _ := strings.Cut(target, ":")
			ch <- kv{key: target, val: queryCountry(cfg, client, host, port)}
		}(ipp)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()

	done := 0
	for item := range ch {
		done++
		results[item.key] = item.val
		pp.update(done, len(ipports), "")
	}
	pp.doneLine()
	return results
}

// fetchAdditionalSource 拉取并解析单个数据源（含重试）
func fetchAdditionalSource(cfg *Config, client *http.Client, rawURL string) []string {
	if rawURL == "" {
		return nil
	}

	// 域名 / 裸 IP 直填源：不做 HTTP 拉取，直接解析成节点。
	// 社区优选域名（cf.090227.xyz 等）必须走这条路径——HTTP 请求它只会拿到
	// 一个网页，解析结果是 0 个节点，属于静默失效。
	if isDirectSource(rawURL) {
		logf("数据源 %s 为域名/裸 IP 直填，解析候选 IP ...", rawURL)
		nodes := resolveDirectSource(cfg, rawURL)
		if len(nodes) == 0 {
			logf("⚠️  %s 未解析出任何节点：域名无 A 记录，或 BARE_IP_DEFAULT_PORT=0 导致裸 IP 被丢弃。", rawURL)
			return nodes
		}
		logf("从 %s 解析出 %d 个节点。", rawURL, len(nodes))
		return nodes
	}

	for attempt := 1; attempt <= cfg.FetchMaxRetries; attempt++ {
		logf("正在请求数据源 %s (尝试 %d/%d) ...", rawURL, attempt, cfg.FetchMaxRetries)
		nodes, err := doFetch(cfg, client, rawURL)
		if err != nil {
			logf("请求或解析失败 (%s): %v", rawURL, err)
			if attempt < cfg.FetchMaxRetries {
				logf("等待 %d 秒后重试...", cfg.FetchRetryDelay)
				sleepSeconds(cfg.FetchRetryDelay)
				continue
			}
			logf("已尝试 %d 次，放弃该数据源。", cfg.FetchMaxRetries)
			return nil
		}
		if len(nodes) == 0 {
			// 请求成功但一个节点都没解析出来。这通常不是「源本身失效」，
			// 而是源站这次吐了空体 / 拦截页 / 结构异常的内容，重试往往能恢复。
			if attempt < cfg.FetchMaxRetries {
				logf("⚠️  本次未解析出节点，%d 秒后重试（源站抖动或返回了非节点内容）。", cfg.FetchRetryDelay)
				sleepSeconds(cfg.FetchRetryDelay)
				continue
			}
			logf("❌ 数据源 %s 请求成功但 %d 次均未解析出节点，本轮跳过（详见上方响应片段）。", rawURL, cfg.FetchMaxRetries)
			return nil
		}
		logf("从 %s 解析出 %d 个节点。", rawURL, len(nodes))
		return nodes
	}
	return nil
}

func doFetch(cfg *Config, client *http.Client, rawURL string) ([]string, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	nodes := parseAdaptive(cfg, string(body))
	if len(nodes) == 0 {
		// 静默失效的头号来源。把「请求成功了，但拿到的到底是什么」打进日志，
		// 否则只能看到一个 0，无从判断是空响应、拦截页，还是解析规则不匹配。
		logf("⚠️  %s 请求成功（HTTP %d，%s，%s）但解析出 0 个节点",
			rawURL, resp.StatusCode, responseContentType(resp), humanBytes(len(body)))
		logf("    响应开头：%s", bodyPreview(body))
	}
	return nodes, nil
}

// responseContentType 取响应 Content-Type（缺失时给出明确占位，避免看着像空值）
func responseContentType(resp *http.Response) string {
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		return ct
	}
	return "Content-Type 缺失"
}

// humanBytes 人类可读的字节数
func humanBytes(n int) string {
	switch {
	case n == 0:
		return "响应体为空（0 字节）"
	case n < 1024:
		return fmt.Sprintf("%d 字节", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB / %d 字节", float64(n)/1024, n)
	default:
		return fmt.Sprintf("%.1f MB / %d 字节", float64(n)/(1<<20), n)
	}
}

// bodyPreview 提取响应体开头片段，用于判断「拿到的到底是什么内容」。
// 空响应体、HTML 拦截页、结构不符的 JSON 都能一眼认出。
func bodyPreview(body []byte) string {
	if len(body) == 0 {
		return "（空响应体，源站这次什么都没返回）"
	}
	// 折叠所有空白，保证日志仍是一行
	flat := strings.Join(strings.Fields(string(body)), " ")
	const maxRunes = 120
	suffix := ""
	if rs := []rune(flat); len(rs) > maxRunes {
		flat = string(rs[:maxRunes])
		suffix = " …（已截断）"
	}
	hint := ""
	switch {
	case strings.HasPrefix(flat, "<"):
		hint = "   ← 是 HTML 页面（拦截页 / 挑战页 / 错误页），不是节点列表"
	case strings.HasPrefix(flat, "{"), strings.HasPrefix(flat, "["):
		hint = "   ← 是 JSON，但字段结构与解析规则不匹配"
	}
	return fmt.Sprintf("%q%s%s", flat, suffix, hint)
}

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
