package main

import (
	"strings"
	"testing"
)

func eqStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s 长度 = %d，期望 %d\ngot:  %v\nwant: %v", label, len(got), len(want), got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %q，期望 %q\n整体 got:  %v\n整体 want: %v", label, i, got[i], want[i], got, want)
			return
		}
	}
}

// ==================== 前置黑名单与 DNS 黑名单对齐 ====================

func TestPreFilterBlockedCountriesAlignment(t *testing.T) {
	cfg := defaultConfig()
	cfg.PreFilterBlockedCountries = []string{"cn"}    // 小写 + 需要规整
	cfg.BlockedCountries = []string{"HK", "US", "CN"} // CN 与前一份重复

	cfg.PreFilterUseDNSBlocklist = true
	eqStrings(t, "并入后", preFilterBlockedCountries(&cfg), []string{"CN", "HK", "US"})

	cfg.PreFilterUseDNSBlocklist = false
	eqStrings(t, "关闭并入后", preFilterBlockedCountries(&cfg), []string{"CN"})

	// 两份都为空 → 空结果（不 panic、不产生空串国家）
	cfg.PreFilterBlockedCountries = nil
	cfg.BlockedCountries = nil
	eqStrings(t, "两份都为空", preFilterBlockedCountries(&cfg), []string{})
}

// TestPreFilterUseDNSBlocklist 端到端确认：并入开启后 HK 在 TCP 测试前就被剔除，
// 关闭后则会一路留到 DNS 环节才被拦（这正是「白测一整轮」的成因）。
func TestPreFilterUseDNSBlocklist(t *testing.T) {
	cfg := defaultConfig()
	cfg.PreFilterPortEnabled = true
	cfg.PreFilterPorts = []int{443}
	cfg.PreFilterBlockedEnabled = true
	cfg.PreFilterBlockedCountries = []string{"CN"}
	cfg.FilterCountriesEnabled = false

	nodes := []string{"1.1.1.1:443#US", "2.2.2.2:443#HK", "3.3.3.3:443#CN", "4.4.4.4:443"}
	raw := append([]string(nil), nodes...)

	cfg.PreFilterUseDNSBlocklist = true
	out := captureStdout(t, func() {
		eqStrings(t, "并入后前置过滤", preFilter(&cfg, nodes), []string{"1.1.1.1:443#US", "4.4.4.4:443"})
	})
	// 无标签节点不应被误杀，且日志要印出实际生效的国家
	if !strings.Contains(out, "HK") || !strings.Contains(out, "CN") {
		t.Errorf("前置黑名单日志应列出实际生效的国家（含 HK/CN），实际:\n%s", out)
	}

	cfg.PreFilterUseDNSBlocklist = false
	captureStdout(t, func() {
		eqStrings(t, "关闭并入后前置过滤", preFilter(&cfg, nodes),
			[]string{"1.1.1.1:443#US", "2.2.2.2:443#HK", "4.4.4.4:443"})
	})

	eqStrings(t, "入参不应被就地改写", nodes, raw)
}

// ==================== 测速前 IPv6 落地过滤 ====================

func TestPreBandwidthIPv6Filter(t *testing.T) {
	cfg := defaultConfig()
	candidates := []string{"1.1.1.1:443#US", "2.2.2.2:443#HK", "3.3.3.3:443#JP"}
	raw := append([]string(nil), candidates...)
	stacks := map[string]string{
		"1.1.1.1:443#US": "ipv4_only",
		"2.2.2.2:443#HK": "ipv6_only",
		// 3.3.3.3 故意缺失：取不到协议栈时不应误杀
	}

	cfg.PreBandwidthIPv6FilterEnabled = true
	out := captureStdout(t, func() {
		eqStrings(t, "开启过滤", preBandwidthIPv6Filter(&cfg, candidates, stacks),
			[]string{"1.1.1.1:443#US", "3.3.3.3:443#JP"})
	})
	if !strings.Contains(out, "3 -> 2") || !strings.Contains(out, "剔除仅 IPv6 可达 1 个") {
		t.Errorf("过滤日志应写明筛减数量，实际:\n%s", out)
	}

	cfg.PreBandwidthIPv6FilterEnabled = false
	eqStrings(t, "关闭过滤", preBandwidthIPv6Filter(&cfg, candidates, stacks), candidates)

	// 可用性检测未启用/整体失败 → stacks 为空，此时必须放行并给出提示
	cfg.PreBandwidthIPv6FilterEnabled = true
	out = captureStdout(t, func() {
		eqStrings(t, "无协议栈信息", preBandwidthIPv6Filter(&cfg, candidates, map[string]string{}), candidates)
	})
	if !strings.Contains(out, "跳过 IPv6 落地过滤") {
		t.Errorf("拿不到协议栈信息时应提示跳过，实际:\n%s", out)
	}

	// 全部被剔除 → 返回空，交由调用方中止流程（不能 panic）
	allIPv6 := map[string]string{
		"1.1.1.1:443#US": "ipv6_only",
		"2.2.2.2:443#HK": "ipv6_only",
		"3.3.3.3:443#JP": "ipv6_only",
	}
	captureStdout(t, func() {
		if got := preBandwidthIPv6Filter(&cfg, candidates, allIPv6); len(got) != 0 {
			t.Errorf("全部为 ipv6_only 时应返回空，实际 %v", got)
		}
	})

	eqStrings(t, "入参不应被就地改写", candidates, raw)
}

// ==================== 测速前 抖动 过滤 ====================

func TestPreBandwidthMaxJitterFilter(t *testing.T) {
	cfg := defaultConfig()
	cfg.PreBandwidthMaxJitterEnabled = true
	cfg.PreBandwidthMaxJitterMs = 50.0

	candidates := []string{"1.1.1.1:443#US", "2.2.2.2:443#HK", "3.3.3.3:443#JP", "4.4.4.4:443"}
	raw := append([]string(nil), candidates...)
	jitter := map[string]float64{
		"1.1.1.1:443#US": 5.0,   // 明显达标
		"2.2.2.2:443#HK": 500.0, // 超标 → 剔除
		"3.3.3.3:443#JP": 50.0,  // 恰好等于阈值：「超过」才筛，应保留
		// 4.4.4.4 故意缺失：取不到抖动时不应误杀
	}

	out := captureStdout(t, func() {
		eqStrings(t, "开启过滤", preBandwidthMaxJitterFilter(&cfg, candidates, jitter),
			[]string{"1.1.1.1:443#US", "3.3.3.3:443#JP", "4.4.4.4:443"})
	})
	if !strings.Contains(out, "4 -> 3") || !strings.Contains(out, "剔除超标 1 个") {
		t.Errorf("过滤日志应写明筛减数量，实际:\n%s", out)
	}
	// 明细行要能定位到具体是哪个 IP 被拦下、抖动多少
	if !strings.Contains(out, "2.2.2.2:443#HK（抖动 500.00 ms）") {
		t.Errorf("明细日志应列出被拦节点及其抖动值，实际:\n%s", out)
	}

	cfg.PreBandwidthMaxJitterEnabled = false
	eqStrings(t, "关闭过滤", preBandwidthMaxJitterFilter(&cfg, candidates, jitter), candidates)

	// HTTP 检测未启用/整体失败 → 抖动表为空，此时必须放行并给出提示
	cfg.PreBandwidthMaxJitterEnabled = true
	out = captureStdout(t, func() {
		eqStrings(t, "无抖动信息", preBandwidthMaxJitterFilter(&cfg, candidates, map[string]float64{}), candidates)
	})
	if !strings.Contains(out, "跳过抖动过滤") {
		t.Errorf("拿不到抖动信息时应提示跳过，实际:\n%s", out)
	}

	// 阈值非正数 → 视为未设置阈值，放行
	cfg.PreBandwidthMaxJitterMs = 0
	out = captureStdout(t, func() {
		eqStrings(t, "阈值为 0", preBandwidthMaxJitterFilter(&cfg, candidates, jitter), candidates)
	})
	if !strings.Contains(out, "跳过抖动过滤") {
		t.Errorf("阈值为 0 时应提示跳过，实际:\n%s", out)
	}

	// 全部超标 → 返回空，交由调用方中止流程（不能 panic）
	cfg.PreBandwidthMaxJitterMs = 1.0
	allBad := map[string]float64{
		"1.1.1.1:443#US": 900, "2.2.2.2:443#HK": 900,
		"3.3.3.3:443#JP": 900, "4.4.4.4:443": 900,
	}
	captureStdout(t, func() {
		if got := preBandwidthMaxJitterFilter(&cfg, candidates, allBad); len(got) != 0 {
			t.Errorf("全部超标时应返回空，实际 %v", got)
		}
	})

	eqStrings(t, "入参不应被就地改写", candidates, raw)
}

// ==================== TCP 延迟 过滤 ====================

func TestPreTCPMaxLatencyFilter(t *testing.T) {
	cfg := defaultConfig()
	cfg.TCPMaxLatencyEnabled = true
	cfg.TCPMaxLatencyMs = 90.0

	// NodeResult.Latency 单位是秒，阈值配置用毫秒 → 90ms 即 0.09s
	results := []*NodeResult{
		{Node: "1.1.1.1:443#US", Latency: 0.065, Success: 1}, // 65ms 达标
		{Node: "2.2.2.2:443#HK", Latency: 0.300, Success: 1}, // 300ms 超标 → 剔除
		{Node: "3.3.3.3:443#JP", Latency: 0.090, Success: 1}, // 恰好等于阈值 → 保留
	}
	raw := append([]*NodeResult(nil), results...)

	names := func(rs []*NodeResult) []string {
		out := make([]string, 0, len(rs))
		for _, r := range rs {
			out = append(out, r.Node)
		}
		return out
	}

	out := captureStdout(t, func() {
		eqStrings(t, "开启过滤", names(preTCPMaxLatencyFilter(&cfg, results)),
			[]string{"1.1.1.1:443#US", "3.3.3.3:443#JP"})
	})
	if !strings.Contains(out, "3 -> 2") || !strings.Contains(out, "剔除超标 1 个") {
		t.Errorf("过滤日志应写明筛减数量，实际:\n%s", out)
	}
	// 明细行要能定位到具体是哪个 IP 被拦下、TCP 延迟多少（毫秒，不是秒）
	if !strings.Contains(out, "2.2.2.2:443#HK（TCP 300.00 ms）") {
		t.Errorf("明细日志应列出被拦节点及其 TCP 延迟（毫秒），实际:\n%s", out)
	}

	cfg.TCPMaxLatencyEnabled = false
	eqStrings(t, "关闭过滤", names(preTCPMaxLatencyFilter(&cfg, results)),
		[]string{"1.1.1.1:443#US", "2.2.2.2:443#HK", "3.3.3.3:443#JP"})

	// 阈值非正数 → 视为未设置阈值，放行
	cfg.TCPMaxLatencyEnabled = true
	cfg.TCPMaxLatencyMs = 0
	out = captureStdout(t, func() {
		eqStrings(t, "阈值为 0", names(preTCPMaxLatencyFilter(&cfg, results)),
			[]string{"1.1.1.1:443#US", "2.2.2.2:443#HK", "3.3.3.3:443#JP"})
	})
	if !strings.Contains(out, "跳过 TCP 延迟过滤") {
		t.Errorf("阈值为 0 时应提示跳过，实际:\n%s", out)
	}

	// 全部超标 → 返回空，交由调用方中止流程（不能 panic）
	cfg.TCPMaxLatencyMs = 1.0
	captureStdout(t, func() {
		if got := preTCPMaxLatencyFilter(&cfg, results); len(got) != 0 {
			t.Errorf("全部超标时应返回空，实际 %d 个", len(got))
		}
	})

	// 入参不应被就地改写（返回的是新切片，元素指针共享是预期的）
	eqStrings(t, "入参不应被就地改写", names(results),
		[]string{"1.1.1.1:443#US", "2.2.2.2:443#HK", "3.3.3.3:443#JP"})
	if len(raw) != 3 {
		t.Errorf("原始切片长度被改写: %d", len(raw))
	}
}

// ==================== 测速前 HTTP 延迟 过滤 ====================

func TestPreBandwidthMaxHTTPLatencyFilter(t *testing.T) {
	cfg := defaultConfig()
	cfg.PreBandwidthMaxHTTPLatencyEnabled = true
	cfg.PreBandwidthMaxHTTPLatencyMs = 180.0

	candidates := []string{"1.1.1.1:443#US", "2.2.2.2:443#HK", "3.3.3.3:443#JP", "4.4.4.4:443"}
	raw := append([]string(nil), candidates...)
	latency := map[string]float64{
		"1.1.1.1:443#US": 120.0, // 明显达标
		"2.2.2.2:443#HK": 800.0, // 超标 → 剔除
		"3.3.3.3:443#JP": 180.0, // 恰好等于阈值：「超过」才筛，应保留
		// 4.4.4.4 故意缺失：取不到延迟时不应误杀
	}

	out := captureStdout(t, func() {
		eqStrings(t, "开启过滤", preBandwidthMaxHTTPLatencyFilter(&cfg, candidates, latency),
			[]string{"1.1.1.1:443#US", "3.3.3.3:443#JP", "4.4.4.4:443"})
	})
	if !strings.Contains(out, "4 -> 3") || !strings.Contains(out, "剔除超标 1 个") {
		t.Errorf("过滤日志应写明筛减数量，实际:\n%s", out)
	}
	// 明细行要能定位到具体是哪个 IP 被拦下、延迟多少
	if !strings.Contains(out, "2.2.2.2:443#HK（HTTP 800.00 ms）") {
		t.Errorf("明细日志应列出被拦节点及其延迟值，实际:\n%s", out)
	}

	cfg.PreBandwidthMaxHTTPLatencyEnabled = false
	eqStrings(t, "关闭过滤", preBandwidthMaxHTTPLatencyFilter(&cfg, candidates, latency), candidates)

	// HTTP 检测未启用/整体失败 → 延迟表为空，此时必须放行并给出提示
	cfg.PreBandwidthMaxHTTPLatencyEnabled = true
	out = captureStdout(t, func() {
		eqStrings(t, "无延迟信息", preBandwidthMaxHTTPLatencyFilter(&cfg, candidates, map[string]float64{}), candidates)
	})
	if !strings.Contains(out, "跳过 HTTP 延迟过滤") {
		t.Errorf("拿不到延迟信息时应提示跳过，实际:\n%s", out)
	}

	// 阈值非正数 → 视为未设置阈值，放行
	cfg.PreBandwidthMaxHTTPLatencyMs = 0
	out = captureStdout(t, func() {
		eqStrings(t, "阈值为 0", preBandwidthMaxHTTPLatencyFilter(&cfg, candidates, latency), candidates)
	})
	if !strings.Contains(out, "跳过 HTTP 延迟过滤") {
		t.Errorf("阈值为 0 时应提示跳过，实际:\n%s", out)
	}

	// 全部超标 → 返回空，交由调用方中止流程（不能 panic）
	cfg.PreBandwidthMaxHTTPLatencyMs = 1.0
	allBad := map[string]float64{
		"1.1.1.1:443#US": 900, "2.2.2.2:443#HK": 900,
		"3.3.3.3:443#JP": 900, "4.4.4.4:443": 900,
	}
	captureStdout(t, func() {
		if got := preBandwidthMaxHTTPLatencyFilter(&cfg, candidates, allBad); len(got) != 0 {
			t.Errorf("全部超标时应返回空，实际 %v", got)
		}
	})

	// 默认配置必须直接可用：开启 + 180ms
	def := defaultConfig()
	if !def.PreBandwidthMaxHTTPLatencyEnabled || def.PreBandwidthMaxHTTPLatencyMs != 180.0 {
		t.Errorf("默认应为 开启 + 180ms，实际 enabled=%v threshold=%g",
			def.PreBandwidthMaxHTTPLatencyEnabled, def.PreBandwidthMaxHTTPLatencyMs)
	}

	eqStrings(t, "入参不应被就地改写", candidates, raw)
}

// ==================== DNS 过滤明细日志 ====================

func TestLogFilterDetail(t *testing.T) {
	// 无命中 → 不打印任何内容
	if out := captureStdout(t, func() { logFilterDetail("IPv6 落地", nil) }); out != "" {
		t.Errorf("无命中时不应打印，实际:\n%s", out)
	}

	// 未超过上限 → 全列出、无省略后缀
	out := captureStdout(t, func() {
		logFilterDetail("国家黑名单", []string{"2.2.2.2:443#HK", "3.3.3.3:443#DE"})
	})
	if !strings.Contains(out, "拦下 2 个") || !strings.Contains(out, "2.2.2.2:443#HK") ||
		!strings.Contains(out, "3.3.3.3:443#DE") || strings.Contains(out, "另有") {
		t.Errorf("明细日志不正确:\n%s", out)
	}

	// 超过上限 → 只列出前 N 条 + 省略后缀
	many := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		many = append(many, "10.0.0."+string(rune('1'+i))+":443#HK")
	}
	out = captureStdout(t, func() { logFilterDetail("IPv6 落地", many) })
	if !strings.Contains(out, "拦下 8 个") || !strings.Contains(out, "另有 3 个") {
		t.Errorf("超长明细应截断并给出剩余数量:\n%s", out)
	}
	if c := strings.Count(out, "10.0.0."); c != filterDetailLimit {
		t.Errorf("应只列出 %d 条明细，实际 %d 条:\n%s", filterDetailLimit, c, out)
	}
}
