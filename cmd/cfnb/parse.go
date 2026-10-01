package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// parseTextNodes 解析纯文本节点列表
func parseTextNodes(cfg *Config, text string) []string {
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
		logf("%d 个节点未能识别或缺少国家，通过可用性检测 API 查询国家...", len(pending))
		resolved := resolveCountriesBatch(cfg, pending)
		for _, ipport := range pending {
			if code, ok := resolved[ipport]; ok && code != "" {
				nodes = append(nodes, ipport+"#"+code)
			}
		}
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

	pp := newProgressPrinter(cfg.ProgressPrintInterval, "[备用API查询]")
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

	for attempt := 1; attempt <= cfg.FetchMaxRetries; attempt++ {
		logf("正在请求数据源 %s (尝试 %d/%d) ...", rawURL, attempt, cfg.FetchMaxRetries)
		nodes, err := doFetch(cfg, client, rawURL)
		if err != nil {
			logf("请求或解析失败 (%s): %v", rawURL, err)
			if attempt < cfg.FetchMaxRetries {
				logf("等待 %d 秒后重试...", cfg.FetchRetryDelay)
				sleepSeconds(cfg.FetchRetryDelay)
			} else {
				logf("已尝试 %d 次，放弃该数据源。", cfg.FetchMaxRetries)
				return nil
			}
			continue
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
	return parseAdaptive(cfg, string(body)), nil
}

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"
