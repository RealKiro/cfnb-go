package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractCountryCode(t *testing.T) {
	cases := []struct {
		label string
		want  string
	}{
		{"US", "US"},
		{"us", ""},      // 小写两位代码不识别（与 Python 版一致）
		{"USA", "US"},   // 三位代码
		{"JP 东京", "JP"}, // 两位代码 + 中文
		{"中国", "CN"},    // 纯中文
		{"香港 HK", "HK"}, // 中文 + 代码
		{"新加坡 Singapore", "SG"},
		{"🇺🇸", "US"}, // emoji 国旗
		{"🇯🇵 Japan", "JP"},
		{"123-US", "US"}, // 带数字前缀噪声
		{"", ""},
		{"unknown-label", ""},
	}

	for _, c := range cases {
		if got := extractCountryCode(c.label); got != c.want {
			t.Errorf("extractCountryCode(%q) = %q, want %q", c.label, got, c.want)
		}
	}
}

func TestIsIPPort(t *testing.T) {
	valid := []string{"1.2.3.4:443", "104.16.0.1:2053"}
	invalid := []string{"1.2.3.4", "1.2.3:443", "[2001:db8::1]:443", "a.b.c.d:443", "1.2.3.4:"}

	for _, v := range valid {
		if !isIPPort(v) {
			t.Errorf("isIPPort(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if isIPPort(v) {
			t.Errorf("isIPPort(%q) = true, want false", v)
		}
	}
}

func TestParseTextNodes(t *testing.T) {
	cfg := defaultConfig()
	// 关闭备用 API 查询路径：所有标签都能直接识别
	text := `
	104.16.0.1:443#US
	104.16.0.2:443#🇯🇵
	104.16.0.3:443#新加坡
	104.16.0.4:443#USA
	104.16.0.5:443#未知标签
	[2001:db8::1]:443#US
	`
	nodes := parseTextNodes(&cfg, text)

	want := []string{
		"104.16.0.1:443#US",
		"104.16.0.2:443#JP",
		"104.16.0.3:443#SG",
		"104.16.0.4:443#US",
	}

	// 未能识别的节点会走 API 查询（测试环境无网络则被丢弃）
	for _, w := range want {
		found := false
		for _, n := range nodes {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("parseTextNodes 缺少期望节点 %q，实际结果: %v", w, nodes)
		}
	}

	for _, n := range nodes {
		if n == "104.16.0.5:443#未知标签" {
			t.Errorf("未识别的标签不应保留原样: %v", nodes)
		}
		if len(n) > 0 && n[0] == '[' {
			t.Errorf("IPv6 节点应被跳过: %v", nodes)
		}
	}
}

// TestWithDefaultPort 验证裸 IP 补默认端口的规整逻辑
func TestWithDefaultPort(t *testing.T) {
	cfg := defaultConfig()

	cases := []struct {
		in     string
		port   int
		want   string
		wantOK bool
	}{
		{"104.17.116.112:443", 443, "104.17.116.112:443", true},   // 已带端口，原样
		{"104.17.116.112:8443", 443, "104.17.116.112:8443", true}, // 非默认端口也不动
		{"104.17.116.112", 443, "104.17.116.112:443", true},       // 裸 IP 补端口
		{"8.212.12.98", 443, "8.212.12.98:443", true},
		{"104.17.116.112", 0, "", false},          // 关闭补全 → 丢弃
		{"[2001:db8::1]:443", 443, "", false},     // IPv6 → 丢弃
		{"example.com", 443, "", false},           // 域名 → 丢弃
		{"104.17.116", 443, "", false},            // 残缺 IPv4 → 丢弃
		{"104.17.116.112:abc", 443, "", false},    // 非法端口 → 丢弃
		{"104.17.116.112:123456", 443, "", false}, // 端口超长（>5 位）→ 丢弃
	}

	for _, c := range cases {
		cfg.BareIPDefaultPort = c.port
		got, ok := withDefaultPort(&cfg, c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("withDefaultPort(%q, port=%d) = (%q, %v)，期望 (%q, %v)",
				c.in, c.port, got, ok, c.want, c.wantOK)
		}
	}
}

// TestParseBareIPSource 验证只吐裸 IP 的数据源（如 ipdb.api.030101.xyz）能被解析出节点。
// 全部用可直接识别的国家标签，避免测试依赖可用性 API 网络请求。
func TestParseBareIPSource(t *testing.T) {
	cfg := defaultConfig()
	cfg.BareIPDefaultPort = 443

	// 形如 "8.212.12.98#"（# 后国家为空）与纯 "104.17.116.112" 都应补上端口
	nodes := parseTextNodes(&cfg, "104.17.117.1#JP\n104.17.117.2#US\n104.17.117.3#SG")
	want := []string{"104.17.117.1:443#JP", "104.17.117.2:443#US", "104.17.117.3:443#SG"}
	for _, w := range want {
		found := false
		for _, n := range nodes {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("裸 IP 源缺少期望节点 %q，实际: %v", w, nodes)
		}
	}

	// 关闭补端口 → 裸 IP 应被整体丢弃
	cfg.BareIPDefaultPort = 0
	if got := parseTextNodes(&cfg, "104.17.117.1#JP"); len(got) != 0 {
		t.Errorf("BareIPDefaultPort=0 时应丢弃裸 IP，实际: %v", got)
	}
}

// TestFetchAllSourcesDedup 验证多源合并去重：
// 同一 ip:port 无论重复出现、写成裸 IP 还是显式端口、标签是否不同，都只保留一次。
// 用本地 httptest 服务提供数据，同时把同一个源配两遍，确认跨源也会去重。
func TestFetchAllSourcesDedup(t *testing.T) {
	payload := strings.Join([]string{
		"104.17.117.1#JP",     // 裸 IP + 标签
		"104.17.117.1#JP",     // 完全重复
		"104.17.117.1:443#JP", // 同一节点的显式端口写法（补端口后应视为同一个）
		"104.17.117.2#US",     // 另一个节点
		"104.17.117.2#SG",     // 同 ip:port、不同标签 → 也应去重
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.BareIPDefaultPort = 443
	cfg.FetchMaxRetries = 1
	// 同一个源写两遍，模拟多源场景
	cfg.AdditionalSources = []SourceConfig{{URL: srv.URL}, {URL: srv.URL}}

	nodes := fetchAllSources(&cfg)

	if len(nodes) != 2 {
		t.Fatalf("期望去重后剩 2 个节点，实际 %d 个: %v", len(nodes), nodes)
	}
	counts := map[string]int{}
	for _, n := range nodes {
		counts[n]++
	}
	for n, c := range counts {
		if c != 1 {
			t.Errorf("节点 %q 出现 %d 次，应为 1 次", n, c)
		}
	}
	if _, ok := counts["104.17.117.1:443#JP"]; !ok {
		t.Errorf("去重结果缺少 104.17.117.1:443#JP，实际: %v", nodes)
	}
	if _, ok := counts["104.17.117.2:443#US"]; !ok {
		t.Errorf("去重结果缺少 104.17.117.2:443#US（应保留先出现的标签），实际: %v", nodes)
	}
}

func TestParseJSONNodes(t *testing.T) {
	cfg := defaultConfig()

	jsonText := `{"nodes":[{"ip":"1.1.1.1","port":443,"country":"us"},{"ip":"2.2.2.2","port":"2053","cc":"JP"}]}`
	nodes := parseAdaptive(&cfg, jsonText)
	if len(nodes) != 2 {
		t.Fatalf("期望解析出 2 个节点，实际 %d: %v", len(nodes), nodes)
	}
	if nodes[0] != "1.1.1.1:443#US" {
		t.Errorf("节点 0 = %q, want %q", nodes[0], "1.1.1.1:443#US")
	}
	if nodes[1] != "2.2.2.2:2053#JP" {
		t.Errorf("节点 1 = %q, want %q", nodes[1], "2.2.2.2:2053#JP")
	}
}

func TestParseAdaptiveMixedText(t *testing.T) {
	cfg := defaultConfig()
	text := "一些无关的前言文字\n# 注释行\n104.16.9.9:443#德国\n结尾说明"
	nodes := parseAdaptive(&cfg, text)
	if len(nodes) != 1 || nodes[0] != "104.16.9.9:443#DE" {
		t.Errorf("混合文本解析结果 = %v, want [104.16.9.9:443#DE]", nodes)
	}
}

func TestConfigDefaultsAndOverride(t *testing.T) {
	cfg := defaultConfig()
	if cfg.GlobalTopN != 15 || cfg.BandwidthCandidat != 150 {
		t.Fatalf("默认配置异常: %+v", cfg)
	}
	// json.Unmarshal 只覆盖出现的字段，未出现字段保持默认
	var c = defaultConfig()
	if err := json.Unmarshal([]byte(`{"GLOBAL_TOP_N": 30, "CF_ENABLED": false}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.GlobalTopN != 30 {
		t.Errorf("GLOBAL_TOP_N = %d, want 30", c.GlobalTopN)
	}
	if c.CFEnabled {
		t.Errorf("CF_ENABLED 应为 false")
	}
	if c.BandwidthCandidat != 150 {
		t.Errorf("未配置字段应保持默认值，BandwidthCandidat = %d", c.BandwidthCandidat)
	}
}

func TestBuildTag(t *testing.T) {
	d := &ipDetails{
		CountryCode: "US",
		Region:      "California",
		City:        "Unknown",
		ASN:         "AS13335",
		ISP:         "Cloudflare, Inc.",
	}
	got := d.buildTag()
	want := "US California AS13335 Cloudflare, Inc."
	if got != want {
		t.Errorf("buildTag() = %q, want %q", got, want)
	}
}

func TestParseOrg(t *testing.T) {
	asn, isp := parseOrg("AS13335 Cloudflare, Inc.")
	if asn != "AS13335" || isp != "Cloudflare, Inc." {
		t.Errorf("parseOrg = (%q, %q)", asn, isp)
	}
	asn, isp = parseOrg("Some Corp")
	// 与 Python 版一致：无 AS 前缀时 ASN=Unknown，ISP 取首个空格之后的部分
	if asn != "Unknown" || isp != "Corp" {
		t.Errorf("parseOrg 无 ASN 时 = (%q, %q)", asn, isp)
	}
	asn, isp = parseOrg("")
	if asn != "Unknown" || isp != "Unknown" {
		t.Errorf("parseOrg 空串时 = (%q, %q)", asn, isp)
	}
}

func TestPreFilter(t *testing.T) {
	cfg := defaultConfig()
	cfg.PreFilterPortEnabled = true
	cfg.PreFilterPorts = []int{443}
	cfg.PreFilterBlockedEnabled = true
	cfg.PreFilterBlockedCountries = []string{"CN"}
	cfg.FilterCountriesEnabled = false

	nodes := []string{
		"1.1.1.1:443#US",
		"1.1.1.2:8443#US", // 端口被过滤
		"1.1.1.3:443#CN",  // 黑名单
		"1.1.1.4:443#JP",
	}
	got := preFilter(&cfg, nodes)
	if len(got) != 2 {
		t.Fatalf("前置过滤后应剩 2 个节点，实际 %v", got)
	}
	if got[0] != "1.1.1.1:443#US" || got[1] != "1.1.1.4:443#JP" {
		t.Errorf("前置过滤结果异常: %v", got)
	}
}

func TestScoreAndSelectGlobal(t *testing.T) {
	cfg := defaultConfig()
	cfg.UseGlobalMode = true
	cfg.GlobalTopN = 2
	cfg.SpeedWeight = 3.0
	cfg.HTTPLatencyWeight = 3.0
	cfg.JitterWeight = 3.0
	cfg.TCPLatencyWeight = 0.0

	bw := []BandwidthResult{
		{Node: "1.1.1.1:443#US", Speed: 10},
		{Node: "1.1.1.2:443#JP", Speed: 50},
		{Node: "1.1.1.3:443#SG", Speed: 30},
	}
	latency := map[string]float64{"1.1.1.1:443#US": 0.05, "1.1.1.2:443#JP": 0.05, "1.1.1.3:443#SG": 0.05}
	httpLat := map[string]float64{"1.1.1.1:443#US": 10, "1.1.1.2:443#JP": 10, "1.1.1.3:443#SG": 10}
	jitter := map[string]float64{"1.1.1.1:443#US": 1, "1.1.1.2:443#JP": 1, "1.1.1.3:443#SG": 1}

	got := scoreAndSelect(&cfg, bw, latency, httpLat, jitter)
	if len(got) != 2 {
		t.Fatalf("应选出 2 个节点，实际 %v", got)
	}
	if got[0] != "1.1.1.2:443#JP" || got[1] != "1.1.1.3:443#SG" {
		t.Errorf("全局模式选择结果异常: %v", got)
	}
}

func TestScoreAndSelectPerCountry(t *testing.T) {
	cfg := defaultConfig()
	cfg.UseGlobalMode = false
	cfg.PerCountryTopN = 1
	cfg.SpeedWeight = 3.0

	bw := []BandwidthResult{
		{Node: "1.1.1.1:443#US", Speed: 10},
		{Node: "1.1.1.2:443#US", Speed: 40},
		{Node: "1.1.1.3:443#JP", Speed: 20},
	}
	latency := map[string]float64{}
	httpLat := map[string]float64{}
	jitter := map[string]float64{}

	got := scoreAndSelect(&cfg, bw, latency, httpLat, jitter)
	if len(got) != 2 {
		t.Fatalf("每国 1 个应选出 2 个节点，实际 %v", got)
	}
	if got[0] != "1.1.1.2:443#US" || got[1] != "1.1.1.3:443#JP" {
		t.Errorf("分国家模式选择结果异常: %v", got)
	}
}

func TestSortNodeResults(t *testing.T) {
	results := []*NodeResult{
		{Node: "a", Latency: 0.3, Success: 1},
		{Node: "b", Latency: 0.1, Success: 2},
		{Node: "c", Latency: 0.2, Success: 2},
	}
	sortNodeResults(results)
	want := []string{"b", "c", "a"}
	for i, w := range want {
		if results[i].Node != w {
			t.Errorf("排序后位置 %d = %q, want %q (%v)", i, results[i].Node, w, results)
		}
	}
}
