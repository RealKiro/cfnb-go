package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// NodeResult TCP 测试结果
type NodeResult struct {
	Node    string
	Latency float64 // 秒
	Country string
	Success int
}

// testTCPLatency 测试 TCP 连接延迟，返回最大延迟与成功次数
func testTCPLatency(ip, port string, timeout float64, probes int) (float64, int) {
	maxLatency := 0.0
	success := 0
	addr := net.JoinHostPort(ip, port)
	dialTimeout := time.Duration(timeout * float64(time.Second))

	for i := 0; i < probes; i++ {
		start := time.Now()
		conn, err := net.DialTimeout("tcp4", addr, dialTimeout)
		if err != nil {
			continue
		}
		latency := time.Since(start).Seconds()
		conn.Close()
		if latency > maxLatency {
			maxLatency = latency
		}
		success++
	}

	if success == 0 {
		return 0, 0
	}
	return maxLatency, success
}

// testNode 单节点测试（含成功率门槛校验）
func testNode(cfg *Config, node string) *NodeResult {
	ip, rest, ok := strings.Cut(node, ":")
	if !ok {
		return nil
	}
	port, country, ok := strings.Cut(rest, "#")
	if !ok {
		return nil
	}

	latency, success := testTCPLatency(ip, port, cfg.Timeout, cfg.TCPProbes)
	if success == 0 {
		return nil
	}
	if cfg.TCPProbes > 0 && float64(success)/float64(cfg.TCPProbes) < cfg.MinSuccessRate {
		return nil
	}
	return &NodeResult{Node: node, Latency: latency, Country: country, Success: success}
}

// ==================== 可用性二次检测 ====================

// AvailabilityResult 可用性检测结果（含落地协议栈信息，供 DNS 更新过滤使用）
type AvailabilityResult struct {
	Node  string
	OK    bool
	Stack string
}

// checkAvailability 调用可用性 API 校验节点
func checkAvailability(cfg *Config, client *http.Client, node string) AvailabilityResult {
	res := AvailabilityResult{Node: node, Stack: "unknown"}

	ip, rest, ok := strings.Cut(node, ":")
	if !ok {
		return res
	}
	port, _, _ := strings.Cut(rest, "#")
	proxyip := ip + ":" + port

	maxAttempts := 1
	retryDelay := 0
	if cfg.AvailabilityInnerRetry {
		maxAttempts = cfg.AvailabilityInnerRetryMax + 1
		retryDelay = cfg.AvailabilityInnerRetryDelay
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		body, err := httpGetJSON(cfg, client, cfg.AvailabilityCheckAPI+"?proxyip="+proxyip)
		if err == nil {
			var data struct {
				Success       bool   `json:"success"`
				InferredStack string `json:"inferred_stack"`
			}
			if json.Unmarshal(body, &data) == nil && data.Success {
				res.OK = true
				res.Stack = orDefault(data.InferredStack, "unknown")
				return res
			}
		}
		if attempt < maxAttempts-1 {
			sleepSeconds(retryDelay)
		}
	}
	return res
}

// availabilityFilterCandidates 单轮并发可用性检测
func availabilityFilterCandidates(cfg *Config, candidates []string) ([]string, map[string]string) {
	if !cfg.TestAvailability || len(candidates) == 0 {
		return candidates, map[string]string{}
	}

	logf("\n对 %d 个候选节点进行可用性二次筛选...", len(candidates))
	client := newHTTPClient(
		time.Duration(cfg.AvailabilityConnectTimout)*time.Second,
		time.Duration(cfg.AvailabilityTimeout)*time.Second,
		true,
	)

	results := parallelRun(candidates, cfg.AvailabilityWorkers, func(node string) AvailabilityResult {
		return checkAvailability(cfg, client, node)
	})

	passed := make([]string, 0, len(results))
	stacks := make(map[string]string, len(results))
	for _, r := range results {
		if r.OK {
			passed = append(passed, r.Node)
			stacks[r.Node] = r.Stack
		}
	}
	return passed, stacks
}

// availabilityFilterWithRetry 多轮可用性检测，全部失败时回退原列表
func availabilityFilterWithRetry(cfg *Config, candidates []string, notifier *Notifier) ([]string, map[string]string) {
	if !cfg.TestAvailability || len(candidates) == 0 {
		return candidates, map[string]string{}
	}

	for attempt := 1; attempt <= cfg.AvailabilityRetryMax; attempt++ {
		logf("\n[可用性检测] 第 %d 轮检测...", attempt)
		passed, stacks := availabilityFilterCandidates(cfg, candidates)
		if len(passed) > 0 {
			logf("可用性检测通过 %d 个节点", len(passed))
			return passed, stacks
		}
		if attempt < cfg.AvailabilityRetryMax {
			logf("本轮可用性检测通过率为 0%%，等待 %d 秒后重试...", cfg.AvailabilityRetryDelay)
			sleepSeconds(cfg.AvailabilityRetryDelay)
		}
	}

	logf("可用性检测经 %d 轮重试后仍无节点通过。", cfg.AvailabilityRetryMax)
	notifier.Send(
		fmt.Sprintf("IP 可用性检测经 %d 轮重试后仍无节点通过，已跳过过滤，使用原候选列表继续。", cfg.AvailabilityRetryMax),
		"可用性检测全部失败",
	)
	return candidates, map[string]string{}
}

// ==================== HTTP 检测（过滤非 Cloudflare 节点 + 延迟/抖动）====================

// HTTPResult HTTP 检测结果
type HTTPResult struct {
	Node    string
	Valid   bool
	Reason  string
	Latency float64 // 毫秒（多次采样的最大值）
	Jitter  float64 // 毫秒（标准差）
}

// checkHTTPServer 探测 /cdn-cgi/trace，要求返回 400 且 server 头以 cloudflare 开头
func checkHTTPServer(cfg *Config, client *http.Client, node string) HTTPResult {
	res := HTTPResult{Node: node}

	ip, rest, ok := strings.Cut(node, ":")
	if !ok {
		res.Reason = "parse_error"
		return res
	}
	port, _, _ := strings.Cut(rest, "#")
	target := fmt.Sprintf("http://%s:%s/cdn-cgi/trace", ip, port)

	rounds := cfg.HTTPJitterSamples
	if rounds < 3 {
		rounds = 3
	}

	latencies := make([]float64, 0, rounds)
	for i := 0; i < rounds; i++ {
		method := http.MethodHead
		if strings.EqualFold(cfg.HTTPTestMethod, "GET") {
			method = http.MethodGet
		}
		req, err := http.NewRequest(method, target, nil)
		if err != nil {
			res.Reason = "parse_error"
			return res
		}
		req.Header.Set("User-Agent", defaultUserAgent)

		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			res.Reason = "connection_error"
			return res
		}
		lat := float64(time.Since(start).Microseconds()) / 1000.0
		status := resp.StatusCode
		server := resp.Header.Get("Server")
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()

		if status != 400 {
			res.Reason = fmt.Sprintf("status_%d", status)
			return res
		}
		if !strings.HasPrefix(strings.ToLower(server), "cloudflare") {
			res.Reason = server
			return res
		}
		latencies = append(latencies, lat)
	}

	if len(latencies) < rounds {
		res.Reason = "not_enough_samples"
		return res
	}

	mean := 0.0
	for _, l := range latencies {
		mean += l
	}
	mean /= float64(len(latencies))

	variance := 0.0
	for _, l := range latencies {
		variance += (l - mean) * (l - mean)
	}
	variance /= float64(len(latencies))

	maxLat := latencies[0]
	for _, l := range latencies {
		if l > maxLat {
			maxLat = l
		}
	}

	res.Valid = true
	res.Reason = "cloudflare"
	res.Latency = maxLat
	res.Jitter = math.Sqrt(variance)
	return res
}

// httpServerFilter 多轮 HTTP 检测，全部失败时降级使用过滤前列表
func httpServerFilter(cfg *Config, candidates []string, notifier *Notifier) ([]string, map[string]float64, map[string]float64) {
	if !cfg.HTTPTestEnabled || len(candidates) == 0 {
		return candidates, map[string]float64{}, map[string]float64{}
	}

	client := newHTTPClient(
		time.Duration(cfg.HTTPTestConnectTimeout)*time.Second,
		time.Duration(cfg.HTTPTestTimeout)*time.Second,
		false, // 强制直连，与 Python 版 proxies=None 一致
	)

	for round := 1; round <= cfg.HTTPTestMaxRounds; round++ {
		logf("\n[HTTP检测] 第 %d 轮检测...", round)
		logf("\n对 %d 个候选节点进行 HTTP 二次筛选...", len(candidates))

		pp := newProgressPrinter(cfg.ProgressPrintInterval, "[HTTP检测]")
		passed, _ := parallelRunProgress(candidates, cfg.HTTPTestWorkers,
			func(node string) (HTTPResult, bool) {
				r := checkHTTPServer(cfg, client, node)
				return r, r.Valid
			}, pp, "通过数量")

		latencyMap := make(map[string]float64, len(passed))
		jitterMap := make(map[string]float64, len(passed))
		passedNodes := make([]string, 0, len(passed))
		for _, r := range passed {
			passedNodes = append(passedNodes, r.Node)
			latencyMap[r.Node] = r.Latency
			jitterMap[r.Node] = r.Jitter
		}

		if len(passedNodes) > 0 {
			logf("HTTP检测通过 %d 个节点", len(passedNodes))
			return passedNodes, latencyMap, jitterMap
		}
		if round < cfg.HTTPTestMaxRounds {
			logf("本轮 HTTP 检测通过率为 0%%，等待 %d 秒后重试...", cfg.HTTPTestRoundDelay)
			sleepSeconds(cfg.HTTPTestRoundDelay)
		}
	}

	notifier.Send(
		fmt.Sprintf("HTTP检测经 %d 轮重试后仍无节点通过，已降级使用过滤前列表。", cfg.HTTPTestMaxRounds),
		"HTTP检测全部失败",
	)
	logf("HTTP检测经 %d 轮重试后仍无节点通过，降级使用过滤前候选列表。", cfg.HTTPTestMaxRounds)
	return candidates, map[string]float64{}, map[string]float64{}
}

// ==================== 带宽测速（Go 原生 HTTP，无需 curl）====================

var (
	bandwidthHTTPPorts  = map[int]bool{80: true, 8080: true, 8880: true, 2052: true, 2082: true, 2086: true, 2095: true}
	bandwidthHTTPSPorts = map[int]bool{443: true, 2053: true, 2083: true, 2087: true, 2096: true, 8443: true}
)

// BandwidthResult 带宽测速结果
type BandwidthResult struct {
	Node  string
	Speed float64 // Mbps
}

// measureBandwidth 通过真实下载测速：将域名解析劫持到被测 IP（等价 curl --resolve）
func measureBandwidth(cfg *Config, node string) BandwidthResult {
	res := BandwidthResult{Node: node}

	ip, rest, ok := strings.Cut(node, ":")
	if !ok {
		return res
	}
	portStr, _, _ := strings.Cut(rest, "#")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return res
	}

	scheme := "https"
	insecure := true
	if bandwidthHTTPPorts[port] {
		scheme = "http"
		insecure = false
	} else if !bandwidthHTTPSPorts[port] {
		scheme, insecure = "https", true // 未知端口默认 https（与 Python 版一致）
	}

	sizeBytes := int(cfg.BandwidthSizeMB * 1024 * 1024)
	if sizeBytes <= 0 {
		sizeBytes = 1024 * 1024
	}
	replacer := strings.NewReplacer(
		"{scheme}", scheme,
		"{port}", portStr,
		"{bytes}", strconv.Itoa(sizeBytes),
	)
	targetURL := replacer.Replace(cfg.BandwidthURLTemplate)

	dialer := &net.Dialer{Timeout: time.Duration(cfg.BandwidthConnectTO) * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// 强制连接被测 IP（等价 --resolve speed.cloudflare.com:port:ip）
			return dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ip, portStr))
		},
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: insecure}, //nolint:gosec // 等价 curl --insecure
		ForceAttemptHTTP2: scheme == "https",
		DisableKeepAlives: true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(cfg.BandwidthTimeout) * time.Second,
	}

	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return res
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return res
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return res
	}

	written, err := io.Copy(io.Discard, resp.Body)
	elapsed := time.Since(start).Seconds()
	if err != nil && written == 0 {
		return res
	}
	if int(written) < sizeBytes || elapsed <= 0 {
		return res
	}

	res.Speed = float64(written) * 8 / (elapsed * 1000 * 1000)
	return res
}

// bandwidthFilter 并发测速并按速度降序返回
func bandwidthFilter(cfg *Config, candidates []string) []BandwidthResult {
	if len(candidates) == 0 {
		return nil
	}

	logf("\n开始带宽测速（对前 %d 个节点，并发 %d，超时 %ds）...", len(candidates), cfg.BandwidthWorkers, cfg.BandwidthTimeout)
	pp := newProgressPrinter(cfg.ProgressPrintInterval, "[带宽测速]")
	results, _ := parallelRunProgress(candidates, cfg.BandwidthWorkers,
		func(node string) (BandwidthResult, bool) {
			r := measureBandwidth(cfg, node)
			return r, r.Speed > 0
		}, pp, "")

	sortBySpeedDesc(results)
	return results
}

// httpGetJSON 简单 GET 并返回响应体（含状态码校验）
func httpGetJSON(cfg *Config, client *http.Client, endpoint string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
