package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ==================== 回归：空标签导致的越界 ====================

func TestFirstField(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"CN", "CN"},
		{" CN ", "CN"},
		{"US-SJC", "US-SJC"},
		{"美国 洛杉矶", "美国"},
	}
	for _, c := range cases {
		if got := firstField(c.in); got != c.want {
			t.Errorf("firstField(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestPreFilterUnlabeledNoPanic 开启 KEEP_UNLABELED_NODES 后会出现没有
// "#国家" 的节点（CF 官方 anycast IP），前置国家黑名单曾在这种情况下
// 用 strings.Fields(tag)[0] 取国家码而越界 panic。这里确认修复且口径正确。
func TestPreFilterUnlabeledNoPanic(t *testing.T) {
	cfg := defaultConfig()
	cfg.PreFilterPortEnabled = true
	cfg.PreFilterPorts = []int{443}
	cfg.PreFilterBlockedEnabled = true
	cfg.PreFilterBlockedCountries = []string{"CN"}
	cfg.FilterCountriesEnabled = false

	nodes := []string{
		"104.17.0.1:443",    // 完全无标签
		"104.17.0.2:443#",   // 标签为空串
		"104.17.0.3:443#  ", // 标签只有空白
		"104.17.0.4:443#CN", // 应被黑名单剔除
		"104.17.0.5:443#US",
	}
	got := preFilter(&cfg, nodes)
	// 无标签的三个 + US 一个 = 4；CN 被剔除
	if len(got) != 4 {
		t.Fatalf("应剩 4 个节点（无标签不参与黑名单判断），实际 %d 个: %v", len(got), got)
	}
	for _, n := range got {
		if strings.Contains(n, "#CN") {
			t.Errorf("被屏蔽国家的节点不应保留: %q", n)
		}
	}
}

// TestApplyCacheEmptyTag 缓存值只有空白时不应回写出 "#" 空标签
func TestApplyCacheEmptyTag(t *testing.T) {
	nodes := []string{"1.1.1.1:443", "1.1.1.2:443#US"}
	applyCache(nodes, map[string]string{"1.1.1.1:443": "   ", "1.1.1.2:443": "JP"})
	if nodes[0] != "1.1.1.1:443" {
		t.Errorf("空白缓存值不应改写节点，实际 %q", nodes[0])
	}
	if nodes[1] != "1.1.1.2:443#JP" {
		t.Errorf("正常缓存值应回写国家码，实际 %q", nodes[1])
	}
}

// ==================== 日志可读性相关的单元测试 ====================

func TestHumanCount(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{1234, "1,234"},
		{14875, "14,875"},
		{1234567, "1,234,567"},
		{-4321, "-4,321"},
	}
	for _, c := range cases {
		if got := humanCount(c.in); got != c.want {
			t.Errorf("humanCount(%d) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestDisplayWidthEmoji emoji 按 2 列计，变体选择符不占宽度
func TestDisplayWidthEmoji(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"🥇", 2},
		{"🛡️", 2}, // 含变体选择符 U+FE0F，不应多算 1 列
		{"📥", 2},
		{"📥抓取", 6},
		{"⚠", 1}, // Ambiguous 区间的图形符号，保守按 1 列
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d，期望 %d", c.in, got, c.want)
		}
	}
	// 表格里 emoji + 中文混排时，padRight 仍能把显示宽度补齐
	s := "📥" + "TCP通过"
	if got := displayWidth(padRight(s, 12)); got != 12 {
		t.Errorf("padRight 后显示宽度应为 12，实际 %d", got)
	}
}

func TestStageIcon(t *testing.T) {
	for _, stage := range []string{"抓取", "去重合并", "前置过滤", "TCP通过", "候选池", "可用通过", "HTTP通过", "带宽通过", "最终入选", "DNS写入"} {
		if ic := stageIcon(stage); ic == "" || ic == "• " {
			t.Errorf("工序 %q 缺少图标映射", stage)
		}
	}
	if ic := stageIcon("未知工序"); ic != "• " {
		t.Errorf("未知工序应回退为默认符号，实际 %q", ic)
	}
}

func TestHumanBytes(t *testing.T) {
	if got := humanBytes(0); !strings.Contains(got, "0 字节") {
		t.Errorf("0 字节应明确说明，实际 %q", got)
	}
	if got := humanBytes(512); got != "512 字节" {
		t.Errorf("512 应显示为 512 字节，实际 %q", got)
	}
	if got := humanBytes(35657); !strings.Contains(got, "KB") || !strings.Contains(got, "35657") {
		t.Errorf("35657 应同时给出 KB 与原始字节，实际 %q", got)
	}
}

func TestBodyPreview(t *testing.T) {
	// 空响应体
	if got := bodyPreview(nil); !strings.Contains(got, "空响应体") {
		t.Errorf("空体应给出明确提示，实际 %q", got)
	}
	// HTML 拦截页
	got := bodyPreview([]byte("<html><head><title>Just a moment...</title></head></html>"))
	if !strings.Contains(got, "HTML") {
		t.Errorf("HTML 内容应给出「拦截页」提示，实际 %q", got)
	}
	// JSON 结构不符
	got = bodyPreview([]byte(`{"ok":false,"msg":"rate limited"}`))
	if !strings.Contains(got, "JSON") {
		t.Errorf("JSON 内容应给出结构提示，实际 %q", got)
	}
	// 正常节点列表（用于对比：不该出现任何提示）
	got = bodyPreview([]byte("38.49.212.71:8443#CA\n103.214.69.199:443#CA"))
	if strings.Contains(got, "拦截") || strings.Contains(got, "不匹配") {
		t.Errorf("正常节点列表不应带异常提示，实际 %q", got)
	}
	// 多行内容折叠成一行 + 超长截断
	got = bodyPreview([]byte(strings.Repeat("a", 400) + "\n\nb"))
	if strings.Contains(got, "\n") {
		t.Errorf("预览必须是单行，实际含换行: %q", got)
	}
	if !strings.Contains(got, "已截断") {
		t.Errorf("超长内容应标注截断，实际 %q", got)
	}
}

// TestFetchZeroNodesIsRetriedAndExplained 请求成功但解析出 0 个节点时：
// 既要重试，也要把「拿到的到底是什么」打出来，避免只看到一个 0。
func TestFetchZeroNodesIsRetriedAndExplained(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		ctype    string
		wantHint string
	}{
		{"空响应体", "", "text/plain; charset=utf-8", "空响应体"},
		{"HTML 拦截页", "<html><body>Just a moment...</body></html>", "text/html", "HTML"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", c.ctype)
				io.WriteString(w, c.body)
			}))
			defer srv.Close()

			cfg := defaultConfig()
			cfg.FetchMaxRetries = 2
			cfg.FetchRetryDelay = 0 // 测试里不真等待
			client := newHTTPClient(2*time.Second, 2*time.Second, false)

			var nodes []string
			out := captureStdout(t, func() {
				nodes = fetchAdditionalSource(&cfg, client, srv.URL+"/all.txt")
			})

			if len(nodes) != 0 {
				t.Fatalf("应解析出 0 个节点，实际 %d 个", len(nodes))
			}
			if n := strings.Count(out, "但解析出 0 个节点"); n != 2 {
				t.Errorf("每次都该打诊断信息（共 2 次），实际 %d 次\n输出:\n%s", n, out)
			}
			if !strings.Contains(out, c.wantHint) {
				t.Errorf("诊断里应给出「%s」提示\n输出:\n%s", c.wantHint, out)
			}
			if !strings.Contains(out, "响应开头") {
				t.Errorf("应打印响应片段\n输出:\n%s", out)
			}
			if !strings.Contains(out, "本轮跳过") {
				t.Errorf("重试耗尽后应明确说明已跳过\n输出:\n%s", out)
			}
		})
	}
}

// TestProgressNonTTY 非终端（docker logs / 重定向）下进度改为换行输出，
// 且只在 10% 档位打印，避免 \r 把多行挤成一行、也避免刷屏。
func TestProgressNonTTY(t *testing.T) {
	if stdoutIsTerminal {
		t.Skip("当前 stdout 是终端，非终端分支不适用")
	}
	pp := newProgressPrinter(0, "🔌 [TCP测试]")
	out := captureStdout(t, func() {
		for i := 1; i <= 100; i++ {
			pp.update(i, 100, " 通过数量：1")
		}
		pp.doneLine()
	})

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 10 {
		t.Errorf("100 次更新应只在 10/20/…/100 各打一行，实际 %d 行:\n%s", len(lines), out)
	}
	if strings.Contains(out, "\r") {
		t.Errorf("非终端输出不应包含回车符，实际:\n%q", out)
	}
	if !strings.Contains(lines[0], "10/100 (10.0%)") {
		t.Errorf("首行应为 10%% 档位，实际 %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "100/100 (100.0%)") {
		t.Errorf("末行应到达 100%%，实际 %q", lines[len(lines)-1])
	}
}

// TestSieveLogStageVisual 工序明细的图标、千分位与告警标记
func TestSieveLogStageVisual(t *testing.T) {
	s := newSieveStats()
	a := s.registerSource("https://a.example/x.txt")
	b := s.registerSource("https://b.example/y.txt")
	c := s.registerSource("https://c.example/z.txt")
	s.claim("1.1.1.1:443", a)
	s.claim("1.1.1.2:443", b)
	s.claim("1.1.1.3:443", c)

	out := captureStdout(t, func() {
		// a 贡献最多、b 少量、c 一个都没有
		s.recordCounts("抓取", map[string]int{a: 12345, b: 5, c: 0})
		// 下一道工序把 b 也刷成 0 → 应标记「已清零」
		s.recordNodes("前置过滤", []string{"1.1.1.1:443#US"})
	})
	for _, want := range []string{
		"📥 抓取", "合计 12,350 个节点", "1 个无数据", "⚠️ 本条数据源本轮无数据",
		"🚧 前置过滤", "保留", "⚠️ 已清零",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("工序日志缺少 %q\n实际输出:\n%s", want, out)
		}
	}
}
