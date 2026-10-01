package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout 捕获 logf / tabwriter 的 stdout 输出
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建管道失败: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	_ = w.Close()
	os.Stdout = old
	return <-done
}

func TestNodeKeyOf(t *testing.T) {
	cases := []struct{ in, want string }{
		{"104.17.117.1:443#JP", "104.17.117.1:443"},
		{"104.17.117.1:443", "104.17.117.1:443"},
		{"104.17.117.1:443#US-SJC", "104.17.117.1:443"},
	}
	for _, c := range cases {
		if got := nodeKeyOf(c.in); got != c.want {
			t.Errorf("nodeKeyOf(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestSourceLabel(t *testing.T) {
	cases := []struct{ in, want string }{
		// 直填源：原名即标签
		{"cf.090227.xyz", "cf.090227.xyz"},
		{"cmcc.090227.xyz:443", "cmcc.090227.xyz:443"},
		{"104.17.117.1", "104.17.117.1"},
		// 普通 URL：取主机名
		{"https://zip.cm.edu.kg/all.txt", "zip.cm.edu.kg"},
		{"https://ipdb.api.030101.xyz/?type=bestproxy&country=true", "ipdb.api.030101.xyz"},
		// GitHub raw：取 owner/repo
		{"https://raw.githubusercontent.com/yuanxiawan/cfipv4db/main/high_score_ips.txt", "yuanxiawan/cfipv4db"},
		{"https://github.com/cmliu/WorkerVless2sub/raw/main/addressesapi.txt", "cmliu/WorkerVless2sub"},
		// GitHub raw 镜像站：owner/repo 被改写进 path，仍应还原成 owner/repo，
		// 否则两个镜像源会退化到同一个「主机名」标签而挤成一列
		{"https://ghproxy.net/https://raw.githubusercontent.com/yuanxiawan/cfipv4db/refs/heads/main/high_score_ips.txt", "yuanxiawan/cfipv4db"},
		{"https://ghproxy.net/https://raw.githubusercontent.com/cmliu/WorkerVless2sub/refs/heads/main/addressesapi.txt", "cmliu/WorkerVless2sub"},
		// 镜像站但路径里没有 GitHub raw 地址 → 回退到主机名
		{"https://mirror.example.com/a/b.txt", "mirror.example.com"},
	}
	for _, c := range cases {
		if got := sourceLabel(c.in); got != c.want {
			t.Errorf("sourceLabel(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// TestSieveRegisterSource 短标签冲突与重复登记的处理
func TestSieveRegisterSource(t *testing.T) {
	s := newSieveStats()
	a := s.registerSource("https://raw.githubusercontent.com/owner1/repo1/main/a.txt")
	b := s.registerSource("https://raw.githubusercontent.com/owner1/repo1/dev/b.txt")

	if a != "owner1/repo1" {
		t.Errorf("第一个源标签应为 owner1/repo1，实际 %q", a)
	}
	// 短标签撞车 → 退到「主机名 + 末段路径」
	if b != "raw.githubusercontent.com/b.txt" {
		t.Errorf("标签冲突时应退到主机名+末段路径，实际 %q", b)
	}

	// 同一 URL 再登记一次 → 加序号
	c := s.registerSource("https://raw.githubusercontent.com/owner1/repo1/main/a.txt")
	if c == a || !strings.HasSuffix(c, "#2") {
		t.Errorf("重复登记应加序号后缀，实际 %q", c)
	}
	if len(s.labels) != 3 {
		t.Errorf("应登记 3 个源，实际 %d 个: %v", len(s.labels), s.labels)
	}
}

// TestSieveLabelFallbackChain 三级标签回退：短标签 → 具体标签 → 完整 URL
func TestSieveLabelFallbackChain(t *testing.T) {
	s := newSieveStats()
	u1 := "https://cdn.example.com/a/list.txt"
	u2 := "https://cdn.example.com/b/list.txt"
	u3 := "https://cdn.example.com/c/list.txt"

	if got := s.registerSource(u1); got != "cdn.example.com" {
		t.Errorf("首个源应为短标签 cdn.example.com，实际 %q", got)
	}
	if got := s.registerSource(u2); got != "cdn.example.com/list.txt" {
		t.Errorf("第二个源应为具体标签 cdn.example.com/list.txt，实际 %q", got)
	}
	// 具体标签也撞车 → 退到完整 URL
	if got := s.registerSource(u3); got != u3 {
		t.Errorf("具体标签也冲突时应退到完整 URL，实际 %q", got)
	}
}

// TestDisplayWidth CJK 显示宽度（中文占 2 列）
func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"抓取", 4},
		{"合计", 4},
		{"TCP通过", 7},
		{"去重合并", 8},
		{"", 0},
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d，期望 %d", c.in, got, c.want)
		}
	}
	// padRight 后的显示宽度应等于目标宽度
	if got := displayWidth(padRight("合计", 10)); got != 10 {
		t.Errorf("padRight 后宽度应为 10，实际 %d", got)
	}
}

// TestSieveClaimFirstWins 归属先到先得
func TestSieveClaimFirstWins(t *testing.T) {
	s := newSieveStats()
	if !s.claim("1.1.1.1:443", "A") {
		t.Error("首次 claim 应成功")
	}
	if s.claim("1.1.1.1:443", "B") {
		t.Error("同一节点被第二个源 claim 应失败")
	}
	if got := s.origin["1.1.1.1:443"]; got != "A" {
		t.Errorf("归属应为先到的 A，实际 %q", got)
	}
}

// TestSieveRecordNodes 按源统计各工序剩余数，且标签变化不影响归属
func TestSieveRecordNodes(t *testing.T) {
	s := newSieveStats()
	a := s.registerSource("https://a.example/x.txt")
	b := s.registerSource("https://b.example/y.txt")

	// 源 A 贡献 3 个、源 B 贡献 2 个（其中 1 个与 A 重复，归属仍算 A）
	s.claim("1.1.1.1:443", a)
	s.claim("1.1.1.2:443", a)
	s.claim("1.1.1.3:443", a)
	s.claim("1.1.1.4:443", b)
	s.claim("1.1.1.1:443", b) // 重复，claim 返回 false（此处只是显式表达语义）
	s.recordCounts("抓取", map[string]int{a: 3, b: 3})

	// 工序 2：节点 3 被过滤；节点 1 的标签被地区校准改写（ip:port 不变）
	s.recordNodes("TCP通过", []string{"1.1.1.1:443#US", "1.1.1.2:443#JP", "1.1.1.4:443#SG"})

	if len(s.stageNames) != 2 {
		t.Fatalf("应记录 2 道工序，实际 %v", s.stageNames)
	}
	if got := s.stageCounts[0]; got[0] != 3 || got[1] != 3 {
		t.Errorf("抓取阶段应为 [3 3]，实际 %v", got)
	}
	if got := s.stageCounts[1]; got[0] != 2 || got[1] != 1 {
		t.Errorf("TCP 阶段应为 [2 1]（标签改写后归属不变），实际 %v", got)
	}
}

// TestSieveUnknownNodeIgnored 不属于任何源的节点（如降级路径的裸 IP）不应被计错列
func TestSieveUnknownNodeIgnored(t *testing.T) {
	s := newSieveStats()
	a := s.registerSource("https://a.example/x.txt")
	s.claim("1.1.1.1:443", a)

	s.recordNodes("DNS写入", []string{"1.1.1.1:443#US", "9.9.9.9"})
	if got := s.stageCounts[0]; got[0] != 1 {
		t.Errorf("未知归属节点应被忽略，实际 %v", got)
	}
}

// TestSievePrintSummary 汇总矩阵的渲染
func TestSievePrintSummary(t *testing.T) {
	s := newSieveStats()
	a := s.registerSource("https://raw.githubusercontent.com/o/r/main/x.txt")
	b := s.registerSource("cf.example.xyz")
	s.claim("1.1.1.1:443", a)
	s.claim("2.2.2.2:443", b)
	s.recordCounts("抓取", map[string]int{a: 100, b: 20})
	s.recordNodes("最终入选", []string{"1.1.1.1:443#US"})

	out := captureStdout(t, func() { s.printSummary() })

	for _, want := range []string{
		"数据源 × 筛选工序 汇总",
		"o/r", // 短标签
		"= https://raw.githubusercontent.com/o/r/main/x.txt", // 图例：短标签 -> 原始 URL
		"抓取", "最终入选",
		"合计",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("汇总输出缺少 %q\n实际输出:\n%s", want, out)
		}
	}
	// 合计行：抓取 120、最终 1
	lines := strings.Split(out, "\n")
	var totalLine string
	for _, l := range lines {
		if strings.HasPrefix(l, "合计") {
			totalLine = l
		}
	}
	if !strings.Contains(totalLine, "120") || !strings.Contains(totalLine, "1") {
		t.Errorf("合计行应为 抓取=120 / 最终入选=1，实际 %q", totalLine)
	}
	// 直填源无需图例（标签即原名）
	if strings.Contains(out, "cf.example.xyz = ") {
		t.Errorf("短标签与原文一致的源不应出现在图例中\n实际输出:\n%s", out)
	}
}
