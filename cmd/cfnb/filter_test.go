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
