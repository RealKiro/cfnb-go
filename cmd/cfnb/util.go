package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// doneGroup 简写别名，便于在并发辅助逻辑中声明计数器
type doneGroup = sync.WaitGroup

// logf 统一日志输出（带时间戳在容器日志中更易排查）
func logf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
}

// firstField 返回按空白切分后的第一段，无内容时返回空串。
// 节点标签在「无国家标签」或「标签为纯空白」时会是空串，
// 直接写 strings.Fields(s)[0] 会越界 panic（切片长度为 0），这里统一兜住。
func firstField(s string) string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// newHTTPClient 构造带连接/读取超时分离的客户端。
// useProxy=true 时遵循系统代理环境变量（API 请求类）；false 时强制直连（测试类请求）。
func newHTTPClient(connectTimeout, readTimeout time.Duration, useProxy bool) *http.Client {
	dialer := &net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}

	tr := &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   readTimeout,
		ResponseHeaderTimeout: readTimeout,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if useProxy && !globalForceDirect {
		tr.Proxy = http.ProxyFromEnvironment
	}

	return &http.Client{
		Transport: tr,
		Timeout:   connectTimeout + readTimeout + 5*time.Second,
	}
}

// applyForceDirect 强制直连：清除所有代理环境变量（对应 Python 版 FORCE_DIRECT）
func applyForceDirect() {
	for _, key := range []string{
		"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy",
	} {
		os.Unsetenv(key)
	}
	os.Setenv("NO_PROXY", "*")
}

// stdoutIsTerminal 判断标准输出是否为交互终端。
// docker logs / 重定向到文件 / 管道都不是终端，此时若进度仍用 \r 覆盖同一行，
// 落盘的日志会把几十次刷新挤成一行（HTML 日志查看器里尤其明显）。
var stdoutIsTerminal = func() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}()

// progressPrinter 进度打印器。
//   - 终端：\r 原地刷新 + 按时间间隔节流（原来的行为，视觉最紧凑）
//   - 非终端：换行输出，且只在跨过 10% 档位或跑到 100% 时打印一行，
//     这样日志里是一串可读的里程碑，而不是一行乱码
type progressPrinter struct {
	mu       sync.Mutex
	last     time.Time
	interval time.Duration
	prefix   string

	nextBucket int // 非终端模式：下一个待打印的百分比档位
}

func newProgressPrinter(interval float64, prefix string) *progressPrinter {
	if interval <= 0 {
		interval = 1
	}
	return &progressPrinter{
		interval:   time.Duration(interval * float64(time.Second)),
		prefix:     prefix,
		nextBucket: 10,
	}
}

func (p *progressPrinter) update(done, total int, extra string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}

	if !stdoutIsTerminal {
		// 非终端：按 10% 档位换行输出，避免刷屏也避免 \r 粘连
		atEnd := done >= total
		if !atEnd && pct < float64(p.nextBucket) {
			return
		}
		for pct >= float64(p.nextBucket) {
			p.nextBucket += 10
		}
		fmt.Printf("%s 进度：%d/%d (%.1f%%)%s\n", p.prefix, done, total, pct, extra)
		return
	}

	// 终端：沿用 \r 原地刷新
	now := time.Now()
	if now.Sub(p.last) < p.interval && done != total {
		return
	}
	p.last = now
	fmt.Printf("\r%s 进度：%d/%d (%.1f%%)%s", p.prefix, done, total, pct, extra)
}

func (p *progressPrinter) doneLine() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !stdoutIsTerminal {
		// 非终端模式下每条进度自带换行，无需再补空行
		return
	}
	fmt.Println()
}

// parallelRun 以指定并发度执行任务，返回结果（保持输入顺序）
func parallelRun[T, R any](items []T, workers int, fn func(T) R) []R {
	if workers < 1 {
		workers = 1
	}
	results := make([]R, len(items))
	if len(items) == 0 {
		return results
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, it T) {
			defer wg.Done()
			defer func() { <-sem }()
			results[idx] = fn(it)
		}(i, item)
	}
	wg.Wait()
	return results
}

// parallelRunIndexed 并发执行并可报告进度；fn 返回结果与是否成功
func parallelRunProgress[T, R any](items []T, workers int, fn func(T) (R, bool), pp *progressPrinter, extraLabel string) ([]R, []T) {
	type pair struct {
		idx int
		res R
		ok  bool
		in  T
	}
	if workers < 1 {
		workers = 1
	}
	ch := make(chan pair, len(items))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for i, item := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, it T) {
			defer wg.Done()
			defer func() { <-sem }()
			r, ok := fn(it)
			ch <- pair{idx: idx, res: r, ok: ok, in: it}
		}(i, item)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var resList []R
	var okInputs []T
	done, okCount := 0, 0
	for p := range ch {
		done++
		if p.ok {
			resList = append(resList, p.res)
			okInputs = append(okInputs, p.in)
			okCount++
		}
		pp.update(done, len(items), fmt.Sprintf(" 通过数量：%d", okCount))
	}
	pp.doneLine()
	return resList, okInputs
}

// sleepSeconds 秒级休眠
func sleepSeconds(sec int) {
	if sec > 0 {
		time.Sleep(time.Duration(sec) * time.Second)
	}
}

// httpGetWithClient 使用给定客户端发起 GET，返回响应体（仅 200 视为成功）
func httpGetWithClient(client *http.Client, endpoint string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return readAllLimit(resp.Body)
}

// readAllLimit 读取响应体，上限 8MB 防止异常数据撑爆内存
func readAllLimit(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 8<<20))
}

// contains 判断字符串是否在切片中
func contains(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

// maxInt 取较大值
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
