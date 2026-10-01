package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ==================== 筛子统计：数据源 × 工序漏斗 ====================
//
// 目标：让每道筛选工序在日志里都能回答两个问题——
//   1. 每个数据源本轮各贡献了多少节点（去重前 / 去重后）？
//   2. 每道工序跑完后，每个数据源还剩多少节点？
//
// 设计口径：
//   - 节点归属键是 ip:port（去掉 # 后的国家标签）。同一 ip:port 出现在
//     多个源时，归属按「第一个贡献它的源」计算——与跨源去重的先到先得
//     口径完全一致，因此各源「去重合并」列相加恒等于总量。
//   - 后续工序（地区校准会改写 # 标签、各测试只做删减不改 ip:port）都
//     不影响归属键，统计可贯穿全流程直至 DNS 写入。
//   - 纯日志功能，无行为变化，不需要配置开关。

// nodeKeyOf 节点归属键：ip:port（与 fetchAllSources 的去重键一致）
func nodeKeyOf(node string) string {
	key, _, _ := strings.Cut(node, "#")
	return key
}

// sourceLabel 源的短显示名（日志与汇总表用，避免整条 URL 撑爆行宽）：
//   - 域名 / 裸 IP 直填源：原名即好标签（cf.090227.xyz）
//   - raw.githubusercontent.com / github.com：取 owner/repo
//   - 其余 URL：取主机名
func sourceLabel(raw string) string {
	s := strings.TrimSpace(raw)
	u, ok := parseSourceURL(s)
	if !ok {
		return s
	}
	host := u.Hostname()
	if host == "raw.githubusercontent.com" || host == "github.com" {
		seg := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(seg) >= 2 {
			return seg[0] + "/" + seg[1]
		}
	}
	return host
}

// sourceLabelDetailed 更具体的标签：主机名 + 末段路径（短标签撞车时启用）
func sourceLabelDetailed(raw string) string {
	s := strings.TrimSpace(raw)
	u, ok := parseSourceURL(s)
	if !ok {
		return s
	}
	host := u.Hostname()
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	last := segs[len(segs)-1]
	if last == "" {
		return host
	}
	return host + "/" + last
}

// parseSourceURL 解析 URL；直填源（不含 ://）返回 false
func parseSourceURL(s string) (*url.URL, bool) {
	if !strings.Contains(s, "://") {
		return nil, false
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return nil, false
	}
	return u, true
}

// displayWidth 估算终端显示宽度：CJK / 全角字符 / emoji 占 2 列。
// 汇总表里中英混排，若按 rune 数对齐会错位（tabwriter 不识别显示宽度）。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// runeWidth 单个字符的显示宽度：
//   - 变体选择符（U+FE0F）与零宽连接符（U+200D）不占宽度
//   - 表情/图标按 2 列计（emoji 通常在终端与浏览器里都渲染成双宽）
//   - CJK 与全角字符按 2 列计，其余 1 列
func runeWidth(r rune) int {
	switch {
	case r == 0xFE0F || r == 0xFE0E || r == 0x200D:
		return 0
	case isWideRune(r):
		return 2
	}
	return 1
}

func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // 韩文字母
		r >= 0x2E80 && r <= 0xA4CF, // CJK 部首 / 汉字 / 日文假名
		r >= 0xAC00 && r <= 0xD7A3, // 韩文音节
		r >= 0xF900 && r <= 0xFAFF, // CJK 兼容汉字
		r >= 0xFE30 && r <= 0xFE6F, // CJK 兼容形式
		r >= 0xFF00 && r <= 0xFF60, // 全角字符
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F000 && r <= 0x1F02F, // 麻将/多米诺等
		r >= 0x1F0A0 && r <= 0x1F0FF,
		r >= 0x1F300 && r <= 0x1F5FF, // 杂项符号与图形
		r >= 0x1F600 && r <= 0x1F64F, // 表情
		r >= 0x1F680 && r <= 0x1F6FF, // 交通与地图符号
		r >= 0x1F7E0 && r <= 0x1F7EB, // 彩色圆/方块
		r >= 0x1F900 && r <= 0x1F9FF, // 补充符号
		r >= 0x1FA70 && r <= 0x1FAFF,
		r >= 0x20000 && r <= 0x3FFFD: // CJK 扩展
		return true
	}
	// 常见图形符号（⚠ ✅ ❌ ⏱…）：Unicode 归类为 Ambiguous，
	// 终端多按 1 列渲染，这里保守按 1 列处理，避免统计偏宽。
	return false
}

// humanCount 给四位以上数字加千分位分隔，长数字在日志里更易读
func humanCount(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)
	if len(s) > 3 {
		var b strings.Builder
		pre := len(s) % 3
		if pre > 0 {
			b.WriteString(s[:pre])
		}
		for i := pre; i < len(s); i += 3 {
			if b.Len() > 0 {
				b.WriteByte(',')
			}
			b.WriteString(s[i : i+3])
		}
		s = b.String()
	}
	if neg {
		return "-" + s
	}
	return s
}

// stageIcons 每道筛选工序的图标（日志里一眼看清当前跑到哪一步）
var stageIcons = map[string]string{
	"抓取":     "📥",
	"去重合并":   "🧹",
	"前置过滤":   "🚧",
	"TCP通过":  "🔌",
	"候选池":    "🎯",
	"可用通过":   "🩺",
	"HTTP通过": "🌐",
	"带宽通过":   "🚀",
	"最终入选":   "🏆",
	"DNS写入":  "📡",
}

func stageIcon(stage string) string {
	if ic, ok := stageIcons[stage]; ok {
		return ic + " "
	}
	return "• "
}

// sieveStats 筛子统计器（非并发安全：全部调用都在主流程单线程中）
type sieveStats struct {
	labels []string          // 源短标签，按配置顺序（含抓取失败的源）
	fulls  map[string]string // 短标签 -> 原始 URL（汇总表图例用）
	origin map[string]string // 节点键（ip:port）-> 源短标签（先到先得）

	stageNames  []string // 已记录的工序名（执行顺序）
	stageCounts [][]int  // stageCounts[工序][源] = 该工序后的剩余节点数
}

func newSieveStats() *sieveStats {
	return &sieveStats{
		fulls:  map[string]string{},
		origin: map[string]string{},
	}
}

// registerSource 登记数据源并返回分配的唯一标签。
// 依次尝试「短标签 → 主机名+末段路径 → 完整 URL」，都被占用时在可用
// 标签后加 #序号（同一个 URL 重复登记即走这条路），保证标签唯一。
func (s *sieveStats) registerSource(raw string) string {
	label := s.freeLabel(raw)
	s.labels = append(s.labels, label)
	s.fulls[label] = raw
	return label
}

func (s *sieveStats) freeLabel(raw string) string {
	for _, base := range []string{sourceLabel(raw), sourceLabelDetailed(raw), raw} {
		if base == "" {
			continue
		}
		prev, taken := s.fulls[base]
		if !taken {
			return base
		}
		if prev == raw {
			// 同一个 URL 已登记过：加序号
			for i := 2; ; i++ {
				cand := fmt.Sprintf("%s#%d", base, i)
				if _, exists := s.fulls[cand]; !exists {
					return cand
				}
			}
		}
	}
	return raw
}

// claim 登记节点归属（先到先得）。返回 false 表示该节点已被其他源占用。
func (s *sieveStats) claim(key, label string) bool {
	if _, exists := s.origin[key]; exists {
		return false
	}
	s.origin[key] = label
	return true
}

// labelIndex 短标签 -> 列下标
func (s *sieveStats) labelIndex() map[string]int {
	idx := make(map[string]int, len(s.labels))
	for i, l := range s.labels {
		idx[l] = i
	}
	return idx
}

// recordCounts 记录一道工序后的「各源剩余数」（counts 的键为源短标签），
// 并即时打印本道工序的筛减明细。
func (s *sieveStats) recordCounts(stage string, counts map[string]int) {
	if len(s.labels) == 0 {
		return
	}
	idx := s.labelIndex()
	row := make([]int, len(s.labels))
	for label, n := range counts {
		if i, ok := idx[label]; ok {
			row[i] = n
		}
	}
	s.logStage(stage, row)
	s.stageNames = append(s.stageNames, stage)
	s.stageCounts = append(s.stageCounts, row)
}

// recordNodes 按节点列表统计各源剩余数并记录
func (s *sieveStats) recordNodes(stage string, nodes []string) {
	if len(s.labels) == 0 {
		return
	}
	counts := make(map[string]int, len(s.labels))
	for _, node := range nodes {
		if label, ok := s.origin[nodeKeyOf(node)]; ok {
			counts[label]++
		}
	}
	s.recordCounts(stage, counts)
}

// logStage 打印本道工序的筛减明细（与上一道工序对比）。
// 第一道（抓取）打印全量源以建立基线、暴露失效源；
// 之后的工序只打印发生了变化的源，减少日志噪音。
func (s *sieveStats) logStage(stage string, row []int) {
	var prev []int
	if len(s.stageCounts) > 0 {
		prev = s.stageCounts[len(s.stageCounts)-1]
	}
	total := 0
	for _, n := range row {
		total += n
	}
	icon := stageIcon(stage)

	if prev == nil {
		dead := 0
		for _, n := range row {
			if n == 0 {
				dead++
			}
		}
		head := fmt.Sprintf("[筛子] %s%s：合计 %s 个节点（%d 个源", icon, stage, humanCount(total), len(s.labels))
		if dead > 0 {
			head += fmt.Sprintf("，⚠️ %d 个无数据", dead)
		}
		logf("\n%s）", head)
		for i, label := range s.labels {
			note := ""
			if row[i] == 0 {
				note = "  ⚠️ 本条数据源本轮无数据"
			}
			logf("       %s  %s%s", padRight(label, s.labelWidth()), humanCount(row[i]), note)
		}
		return
	}

	prevTotal := 0
	for _, n := range prev {
		prevTotal += n
	}
	keep := ""
	if prevTotal > 0 {
		keep = fmt.Sprintf("  保留 %.1f%%", float64(total)/float64(prevTotal)*100)
	}
	logf("\n[筛子] %s%s：合计 %s → %s%s", icon, stage, humanCount(prevTotal), humanCount(total), keep)

	changed := 0
	for i, label := range s.labels {
		if row[i] == prev[i] {
			continue
		}
		changed++
		note := ""
		if row[i] == 0 && prev[i] > 0 {
			note = "  ⚠️ 已清零"
		}
		logf("       %s  %s → %s%s", padRight(label, s.labelWidth()), humanCount(prev[i]), humanCount(row[i]), note)
	}
	if changed == 0 {
		logf("       （各数据源均无变化）")
	}
}

// labelWidth 标签列的最大显示宽度（用于对齐）
func (s *sieveStats) labelWidth() int {
	w := 0
	for _, l := range s.labels {
		if dw := displayWidth(l); dw > w {
			w = dw
		}
	}
	return w
}

// padRight 按显示宽度右侧补空格（CJK 记 2 列）
func padRight(s string, w int) string {
	if pad := w - displayWidth(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// printSummary 打印「数据源 × 工序」汇总矩阵（run 结束时执行，defer 调用）
func (s *sieveStats) printSummary() {
	if len(s.labels) == 0 || len(s.stageCounts) == 0 {
		return
	}

	logf("\n============ 📊 数据源 × 筛选工序 汇总 ============")
	for _, label := range s.labels {
		if full := s.fulls[label]; full != label {
			logf("  %s = %s", label, full)
		}
	}

	// 组装表格：表头 + 每源一行 + 合计行
	rows := make([][]string, 0, len(s.labels)+2)
	head := make([]string, 0, len(s.stageNames)+1)
	head = append(head, "数据源")
	for _, st := range s.stageNames {
		head = append(head, strings.TrimSpace(stageIcon(st))+st)
	}
	rows = append(rows, head)
	for i, label := range s.labels {
		row := make([]string, 1, len(s.stageNames)+1)
		row[0] = label
		for _, counts := range s.stageCounts {
			row = append(row, humanCount(counts[i]))
		}
		rows = append(rows, row)
	}
	totalRow := make([]string, 1, len(s.stageNames)+1)
	totalRow[0] = "合计"
	for _, counts := range s.stageCounts {
		sum := 0
		for _, n := range counts {
			sum += n
		}
		totalRow = append(totalRow, humanCount(sum))
	}
	rows = append(rows, totalRow)

	// 列宽按显示宽度计算
	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for c, cell := range row {
			if w := displayWidth(cell); w > widths[c] {
				widths[c] = w
			}
		}
	}

	var sep strings.Builder
	sep.WriteString(strings.Repeat("-", widths[0]+2))
	for _, w := range widths[1:] {
		sep.WriteString(strings.Repeat("-", w+2))
	}

	for i, row := range rows {
		var b strings.Builder
		for c, cell := range row {
			pad := widths[c] - displayWidth(cell)
			if c == 0 {
				b.WriteString(cell)
				b.WriteString(strings.Repeat(" ", pad+2))
			} else {
				b.WriteString(strings.Repeat(" ", pad+2))
				b.WriteString(cell)
			}
		}
		logf("%s", strings.TrimRight(b.String(), " "))
		if i == 0 {
			logf("%s", sep.String()) // 表头下加分隔线
		}
		if i == len(rows)-2 {
			logf("%s", sep.String()) // 合计行前加分隔线
		}
	}

	// 失效源单列出来，一眼看到「哪个源本轮没产出」
	var dead []string
	for i, label := range s.labels {
		if s.stageCounts[0][i] == 0 {
			dead = append(dead, label)
		}
	}
	if len(dead) > 0 {
		logf("⚠️  以下数据源本轮未解析出任何节点（源站失效 / 被限流 / 网络不可达）:")
		for _, label := range dead {
			logf("      · %s", label)
		}
	} else {
		logf("✅ 全部数据源本轮均有产出。")
	}
	logf("💡 每列为该工序结束后的剩余节点数，合计行即当轮总量。")
}
