package main

import "testing"

// bw 构造带宽测速结果（顺序即速度降序）
func bw(nodes ...string) []BandwidthResult {
	out := make([]BandwidthResult, 0, len(nodes))
	for i, n := range nodes {
		out = append(out, BandwidthResult{Node: n, Speed: float64(100 - i*10)})
	}
	return out
}

func bwNodes(rs []BandwidthResult) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Node)
	}
	return out
}

// TestOrderCandidatesForDNSPreferredFirst 是本次修复的核心：
// DNS 的挑选顺序必须与 ip.txt（加权分降序）一致，而不是速度降序。
func TestOrderCandidatesForDNSPreferredFirst(t *testing.T) {
	// 速度降序：A 最快；但加权分降序（ip.txt 顺序）是 C, A
	results := bw("A", "B", "C", "D")
	got := bwNodes(orderCandidatesForDNS(results, []string{"C", "A"}))

	// 优选项按 ip.txt 顺序前置，其余按速度降序补位
	eqStrings(t, "DNS 候选顺序", got, []string{"C", "A", "B", "D"})

	// 复现原始缺陷现场：带宽通过数 > GLOBAL_TOP_N 时，
	// 旧实现只看前 N 个（速度序）→ 会把 ip.txt 的第 1 名排除在外
	targetCount := 2
	eqStrings(t, "修复后取前 2", got[:targetCount], []string{"C", "A"})
	eqStrings(t, "旧实现取前 2（速度序，集合都不同）", bwNodes(results)[:targetCount], []string{"A", "B"})
}

func TestOrderCandidatesForDNSPreferredAbsentIgnored(t *testing.T) {
	results := bw("A", "B", "C")
	// preferred 里混入不在测速结果中的节点（理论上不该有）→ 忽略，不产生空入口
	eqStrings(t, "忽略不存在的优选项", bwNodes(orderCandidatesForDNS(results, []string{"X", "B"})), []string{"B", "A", "C"})
}

func TestOrderCandidatesForDNSNoPreference(t *testing.T) {
	results := bw("A", "B", "C")
	// 没有优选名单（如降级路径）→ 保持原序，行为与修复前一致
	eqStrings(t, "无优选名单", bwNodes(orderCandidatesForDNS(results, nil)), []string{"A", "B", "C"})
	eqStrings(t, "空优选名单", bwNodes(orderCandidatesForDNS(results, []string{})), []string{"A", "B", "C"})
}

func TestOrderCandidatesForDNSEmpty(t *testing.T) {
	if got := orderCandidatesForDNS(nil, []string{"A"}); got != nil {
		t.Errorf("空测速结果应返回 nil，实际 %v", got)
	}
	if got := orderCandidatesForDNS([]BandwidthResult{}, []string{"A"}); len(got) != 0 {
		t.Errorf("空测速结果应返回空切片，实际 %v", got)
	}
}

func TestOrderCandidatesForDNSIdentity(t *testing.T) {
	// 集合一致且顺序一致时（带宽通过数 == GLOBAL_TOP_N，即实测最常遇到的情况）不应改变任何东西
	results := bw("A", "B", "C")
	eqStrings(t, "顺序保持不变", bwNodes(orderCandidatesForDNS(results, []string{"A", "B", "C"})), []string{"A", "B", "C"})
}

// TestDefaultBandwidthCandidatesCoversIPv6Sieve 守住默认值：
// 候选池必须显著大于 GLOBAL_TOP_N，否则 IPv6 落地过滤（实测砍掉 59%~92%）之后
// 根本凑不满最终优选数量——这是默认值从 150 提到 300 的原因。
func TestDefaultBandwidthCandidatesCoversIPv6Sieve(t *testing.T) {
	cfg := defaultConfig()
	if cfg.BandwidthCandidat < cfg.GlobalTopN*10 {
		t.Errorf("BANDWIDTH_CANDIDATES 默认值 %d 相对 GLOBAL_TOP_N %d 偏小：IPv6 过滤后可能凑不满最终名单",
			cfg.BandwidthCandidat, cfg.GlobalTopN)
	}
}
