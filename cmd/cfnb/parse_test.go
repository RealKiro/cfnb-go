package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	sieve := newSieveStats()
	nodes := fetchAllSources(&cfg, sieve)

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

	// 筛子统计：同一 URL 配两遍应分配两个不冲突的标签，
	// 且「抓取」记原始条数（各 5 条），「去重合并」只记给先到先得的源
	if len(sieve.labels) != 2 {
		t.Fatalf("期望登记 2 个源标签，实际 %d 个: %v", len(sieve.labels), sieve.labels)
	}
	if sieve.labels[0] == sieve.labels[1] {
		t.Errorf("重复登记的源标签应加序号区分，实际都是 %q", sieve.labels[0])
	}
	if sieve.stageNames[0] != "抓取" {
		t.Errorf("第一道工序应为「抓取」，实际 %q", sieve.stageNames[0])
	}
	if got := sieve.stageCounts[0]; len(got) != 2 || got[0] != 5 || got[1] != 5 {
		t.Errorf("抓取阶段应记录 [5 5]（各源原始 5 条），实际 %v", got)
	}
	sieve.recordNodes("去重合并", nodes)
	if got := sieve.stageCounts[1]; len(got) != 2 || got[0] != 2 || got[1] != 0 {
		t.Errorf("去重后应全部归给先出现的源 [2 0]，实际 %v", got)
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
	// BandwidthCandidat 默认 300：IPv6 落地过滤实测会砍掉候选池的 59%~92%，
	// 150 时最终常凑不满 GLOBAL_TOP_N
	if cfg.GlobalTopN != 15 || cfg.BandwidthCandidat != 300 {
		t.Fatalf("默认配置异常: %+v", cfg)
	}
	// 三道「延迟/抖动」闸默认全部开启：TCP 90ms / HTTP 180ms / 抖动 30ms
	if !cfg.TCPMaxLatencyEnabled || cfg.TCPMaxLatencyMs != 90.0 {
		t.Errorf("TCP 延迟过滤默认值应为 开启 + 90ms，实际 enabled=%v threshold=%g",
			cfg.TCPMaxLatencyEnabled, cfg.TCPMaxLatencyMs)
	}
	if !cfg.PreBandwidthMaxHTTPLatencyEnabled || cfg.PreBandwidthMaxHTTPLatencyMs != 180.0 {
		t.Errorf("HTTP 延迟过滤默认值应为 开启 + 180ms，实际 enabled=%v threshold=%g",
			cfg.PreBandwidthMaxHTTPLatencyEnabled, cfg.PreBandwidthMaxHTTPLatencyMs)
	}
	if !cfg.PreBandwidthMaxJitterEnabled || cfg.PreBandwidthMaxJitterMs != 30.0 {
		t.Errorf("抖动过滤默认值应为 开启 + 30ms，实际 enabled=%v threshold=%g",
			cfg.PreBandwidthMaxJitterEnabled, cfg.PreBandwidthMaxJitterMs)
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
	if c.BandwidthCandidat != 300 {
		t.Errorf("未配置字段应保持默认值，BandwidthCandidat = %d", c.BandwidthCandidat)
	}
	if !c.TCPMaxLatencyEnabled || c.TCPMaxLatencyMs != 90.0 {
		t.Errorf("未配置字段应保持默认值，TCP 延迟过滤 = enabled:%v threshold:%g",
			c.TCPMaxLatencyEnabled, c.TCPMaxLatencyMs)
	}
	if !c.PreBandwidthMaxHTTPLatencyEnabled || c.PreBandwidthMaxHTTPLatencyMs != 180.0 {
		t.Errorf("未配置字段应保持默认值，HTTP 延迟过滤 = enabled:%v threshold:%g",
			c.PreBandwidthMaxHTTPLatencyEnabled, c.PreBandwidthMaxHTTPLatencyMs)
	}
	if !c.PreBandwidthMaxJitterEnabled || c.PreBandwidthMaxJitterMs != 30.0 {
		t.Errorf("未配置字段应保持默认值，抖动过滤 = enabled:%v threshold:%g",
			c.PreBandwidthMaxJitterEnabled, c.PreBandwidthMaxJitterMs)
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

// TestParseSpacedLabel 验证 "IP # LABEL" 这种带空格的标签写法能被正确识别。
// yuanxiawan/cfipv4db 的 high_score_ips.txt 就是这种格式（"104.24.76.225 # US-SJC"）：
// 若不先规整，"#" 与标签会被空白切成独立 token，IP 会退化成无标签节点、标签丢失。
func TestParseSpacedLabel(t *testing.T) {
	cfg := defaultConfig()
	cfg.KeepUnlabeledNodes = true

	text := "104.24.76.225 # US-SJC\n162.159.147.80 # US-SJC\n108.162.195.238 # UNK-UNK"
	nodes := parseTextNodes(&cfg, text)

	if len(nodes) != 3 {
		t.Fatalf("期望 3 个节点，实际 %d 个: %v", len(nodes), nodes)
	}

	got := map[string]bool{}
	for _, n := range nodes {
		got[n] = true
	}
	// US-SJC 应被识别出 US 并补上 443；UNK-UNK 认不出国家 → 保留为无标签形式
	for _, w := range []string{"104.24.76.225:443#US", "162.159.147.80:443#US", "108.162.195.238:443"} {
		if !got[w] {
			t.Errorf("缺少期望节点 %q，实际结果: %v", w, nodes)
		}
	}
}

// TestKeepUnlabeledNodes 验证 KEEP_UNLABELED_NODES 开关的两侧行为。
func TestKeepUnlabeledNodes(t *testing.T) {
	// 关闭时：无标签节点走可用性 API 查询国家，这里用返回空结果的假 API 模拟"查不出"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"probe_results":{}}`)
	}))
	defer srv.Close()

	off := defaultConfig()
	off.KeepUnlabeledNodes = false
	off.AvailabilityCheckAPI = srv.URL
	if got := parseTextNodes(&off, "104.17.0.1"); len(got) != 0 {
		t.Errorf("开关关闭且 API 查不出国家时，节点应被丢弃，实际: %v", got)
	}

	on := defaultConfig()
	on.KeepUnlabeledNodes = true
	got := parseTextNodes(&on, "104.17.0.1")
	if len(got) != 1 || got[0] != "104.17.0.1:443" {
		t.Errorf("开关开启时应保留为 ip:port 形式，实际: %v", got)
	}
}

// TestCheckAvailabilitySkipsUnlabeled 验证开启开关后，无标签节点不再走「反代可用性」API。
// API 指向一个必然连不上的地址：若仍发起请求，结果必为 OK=false。
func TestCheckAvailabilitySkipsUnlabeled(t *testing.T) {
	cfg := defaultConfig()
	client := newHTTPClient(time.Second, time.Second, true)
	cfg.AvailabilityCheckAPI = "http://127.0.0.1:9/never"
	cfg.AvailabilityInnerRetry = false // 关闭内部重试，避免测试等待

	cfg.KeepUnlabeledNodes = true
	if res := checkAvailability(&cfg, client, "104.17.0.1:443"); !res.OK {
		t.Errorf("无标签节点应跳过反代可用性检测（OK=true），实际: %+v", res)
	}

	cfg.KeepUnlabeledNodes = false
	if res := checkAvailability(&cfg, client, "104.17.0.1:443"); res.OK {
		t.Errorf("关闭开关时仍应走 API 检测（此处 API 不可达，应为 OK=false），实际: %+v", res)
	}
}

// TestIsDirectSource 验证「域名 / 裸 IP 直填源」的识别规则
func TestIsDirectSource(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://zip.cm.edu.kg/all.txt", false},
		{"http://example.com/list.txt", false},
		{"https://ipdb.api.030101.xyz/?type=bestproxy&country=true", false},
		{"cf.090227.xyz", true},
		{"cmcc.090227.xyz", true},
		{"cf.090227.xyz:8443", true},
		{"1.2.3.4", true},
		{"1.2.3.4:8443", true},
		{"example.com/path", false}, // 带路径 → 不按纯域名处理
		{"notadomain", false},       // 不含点 → 不按域名处理
		{"", false},
	}
	for _, c := range cases {
		if got := isDirectSource(c.in); got != c.want {
			t.Errorf("isDirectSource(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

// TestResolveDirectSource 验证直填源解析（走裸 IP 分支，不依赖 DNS 网络）
func TestResolveDirectSource(t *testing.T) {
	cfg := defaultConfig()

	if got := resolveDirectSource(&cfg, "1.2.3.4:8443"); len(got) != 1 || got[0] != "1.2.3.4:8443" {
		t.Errorf("带端口的裸 IP 应原样使用，实际: %v", got)
	}
	if got := resolveDirectSource(&cfg, "1.2.3.4"); len(got) != 1 || got[0] != "1.2.3.4:443" {
		t.Errorf("裸 IP 应补默认端口，实际: %v", got)
	}

	cfg.BareIPDefaultPort = 0
	if got := resolveDirectSource(&cfg, "1.2.3.4"); len(got) != 0 {
		t.Errorf("BARE_IP_DEFAULT_PORT=0 且源内无端口时应返回空，实际: %v", got)
	}
}

// TestDeployConfigJSONIsValid 校验随仓库分发的 deploy/app/config.json。
// 它是 Release 产物与容器默认配置的来源；一旦写坏，程序会静默回退内置默认值
// 而不报错，问题会被完全掩盖，所以这里显式把关。
func TestDeployConfigJSONIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "deploy", "app", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("%s 不是合法 JSON 配置: %v", path, err)
	}
	if len(cfg.AdditionalSources) == 0 {
		t.Errorf("%s 的 ADDITIONAL_SOURCES 为空", path)
	}
	for _, s := range cfg.AdditionalSources {
		if strings.TrimSpace(s.URL) == "" {
			t.Errorf("%s 中存在 url 为空的数据源项", path)
		}
	}
	if cfg.BareIPDefaultPort <= 0 {
		t.Errorf("%s 的 BARE_IP_DEFAULT_PORT 应大于 0（裸 IP / 域名直填源依赖它），实际 %d",
			path, cfg.BareIPDefaultPort)
	}
	if !cfg.KeepUnlabeledNodes {
		t.Logf("提示：%s 中 KEEP_UNLABELED_NODES=false，CF 官方 IP 源（cfipv4db、社区优选域名）将失效", path)
	}
	// 开启某道过滤但没给有效阈值 → 该道过滤会被静默跳过，属配置矛盾，提前拦下
	if cfg.TCPMaxLatencyEnabled && cfg.TCPMaxLatencyMs <= 0 {
		t.Errorf("%s 开启了 TCP 延迟过滤但 TCP_MAX_LATENCY_MS = %g，该道过滤会被跳过",
			path, cfg.TCPMaxLatencyMs)
	}
	if cfg.PreBandwidthMaxHTTPLatencyEnabled && cfg.PreBandwidthMaxHTTPLatencyMs <= 0 {
		t.Errorf("%s 开启了 HTTP 延迟过滤但 PRE_BANDWIDTH_MAX_HTTP_LATENCY_MS = %g，该道过滤会被跳过",
			path, cfg.PreBandwidthMaxHTTPLatencyMs)
	}
	if cfg.PreBandwidthMaxJitterEnabled && cfg.PreBandwidthMaxJitterMs <= 0 {
		t.Errorf("%s 开启了抖动过滤但 PRE_BANDWIDTH_MAX_JITTER_MS = %g，该道过滤会被跳过",
			path, cfg.PreBandwidthMaxJitterMs)
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
