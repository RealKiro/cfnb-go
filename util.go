package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// doneGroup 简写别名，便于在并发辅助逻辑中声明计数器
type doneGroup = sync.WaitGroup

// logf 统一日志输出（带时间戳在容器日志中更易排查）
func logf(format string, args ...any) {
	fmt.Printf(format+"\n", args...)
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

// progressPrinter 进度打印器（按间隔节流，避免频繁 I/O）
type progressPrinter struct {
	mu       sync.Mutex
	last     time.Time
	interval time.Duration
	prefix   string
}

func newProgressPrinter(interval float64, prefix string) *progressPrinter {
	if interval <= 0 {
		interval = 1
	}
	return &progressPrinter{
		interval: time.Duration(interval * float64(time.Second)),
		prefix:   prefix,
	}
}

func (p *progressPrinter) update(done, total int, extra string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if now.Sub(p.last) < p.interval && done != total {
		return
	}
	p.last = now
	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}
	fmt.Printf("\r%s 进度：%d/%d (%.1f%%)%s", p.prefix, done, total, pct, extra)
}

func (p *progressPrinter) doneLine() {
	p.mu.Lock()
	defer p.mu.Unlock()
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
