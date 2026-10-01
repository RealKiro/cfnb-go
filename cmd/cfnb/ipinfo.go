package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ==================== IP 地区校准（ipinfo.io）====================

type ipDetails struct {
	CountryCode string
	Region      string
	City        string
	ASN         string
	ISP         string
}

// buildTag 拼接标签：国家码 地区 城市 ASN ISP（跳过 Unknown）
func (d *ipDetails) buildTag() string {
	parts := []string{d.CountryCode}
	for _, v := range []string{d.Region, d.City, d.ASN, d.ISP} {
		if v != "" && v != "Unknown" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

// ipInfoClient 带 token 轮换、限速与冷却的 ipinfo.io 客户端
type ipInfoClient struct {
	tokens           []string
	failureThreshold int
	minInterval      float64

	mu            sync.Mutex
	cur           int
	failures      []int
	cooldownUntil []float64
	lastRequest   []float64
	rateLocks     []*sync.Mutex

	sem         chan struct{}
	httpClient  *http.Client
	maxAttempts int
}

func newIPInfoClient(cfg *Config, tokens []string) *ipInfoClient {
	n := len(tokens)
	threshold := int(cfg.TokenFailureThreshold)
	if threshold < 2 {
		threshold = 2
	}
	maxAttempts := 3
	if n > maxAttempts {
		maxAttempts = n
	}
	if maxAttempts > 10 {
		maxAttempts = 10
	}

	c := &ipInfoClient{
		tokens:           tokens,
		failureThreshold: threshold,
		minInterval:      cfg.IPCalibrationMinInterval,
		failures:         make([]int, n),
		cooldownUntil:    make([]float64, n),
		lastRequest:      make([]float64, n),
		rateLocks:        make([]*sync.Mutex, n),
		sem:              make(chan struct{}, maxInt(1, cfg.IPCalibrationConcurrency)),
		httpClient:       newHTTPClient(5*time.Second, 5*time.Second, !cfg.ForceDirect),
		maxAttempts:      maxAttempts,
	}
	for i := range c.rateLocks {
		c.rateLocks[i] = &sync.Mutex{}
	}
	return c
}

// acquireToken 环形轮询选取可用 token；全部不可用时选失败次数最少的
func (c *ipInfoClient) acquireToken() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := float64(time.Now().UnixNano()) / 1e9
	n := len(c.tokens)
	for i := 0; i < n; i++ {
		c.cur = (c.cur + 1) % n
		idx := c.cur
		if c.failures[idx] < c.failureThreshold && c.cooldownUntil[idx] <= now {
			return idx
		}
	}
	best := 0
	for i := 1; i < n; i++ {
		if c.failures[i] < c.failures[best] {
			best = i
		}
	}
	c.cur = best
	return best
}

// rateLimit 保证同一 token 的请求间隔不小于 minInterval
func (c *ipInfoClient) rateLimit(idx int) {
	lock := c.rateLocks[idx]
	lock.Lock()
	defer lock.Unlock()

	now := float64(time.Now().UnixNano()) / 1e9
	if wait := c.lastRequest[idx] + c.minInterval - now; wait > 0 {
		time.Sleep(time.Duration(wait * float64(time.Second)))
	}
	c.lastRequest[idx] = float64(time.Now().UnixNano()) / 1e9
}

func (c *ipInfoClient) setCooldown(idx int, seconds float64) {
	c.mu.Lock()
	c.cooldownUntil[idx] = float64(time.Now().UnixNano())/1e9 + seconds
	c.mu.Unlock()
}

func (c *ipInfoClient) bumpFailure(idx int) {
	c.mu.Lock()
	c.failures[idx]++
	c.mu.Unlock()
}

func (c *ipInfoClient) resetFailure(idx int) {
	c.mu.Lock()
	c.failures[idx] = 0
	c.mu.Unlock()
}

// getDetails 查询单个 IP 的地区信息（含 token 轮换与重试）
func (c *ipInfoClient) getDetails(ip string) *ipDetails {
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		idx := c.acquireToken()
		endpoint := fmt.Sprintf("https://ipinfo.io/%s/json?token=%s", ip, c.tokens[idx])

		c.rateLimit(idx)

		c.sem <- struct{}{}
		body, err := httpGetWithClient(c.httpClient, endpoint)
		<-c.sem

		if err != nil {
			if strings.Contains(err.Error(), "HTTP 429") {
				c.setCooldown(idx, 1.0)
				continue
			}
			if strings.Contains(err.Error(), "HTTP 401") || strings.Contains(err.Error(), "HTTP 403") {
				c.bumpFailure(idx)
				continue
			}
			// 网络抖动：短冷却，不计 token 失效
			c.setCooldown(idx, 0.3)
			continue
		}

		var data struct {
			Country string `json:"country"`
			Region  string `json:"region"`
			City    string `json:"city"`
			Org     string `json:"org"`
		}
		if err := json.Unmarshal(body, &data); err != nil {
			c.setCooldown(idx, 0.3)
			continue
		}

		c.resetFailure(idx)
		if data.Country == "" || data.Country == "Unknown" {
			continue
		}

		asn, isp := parseOrg(data.Org)
		return &ipDetails{
			CountryCode: data.Country,
			Region:      orDefault(data.Region, "Unknown"),
			City:        orDefault(data.City, "Unknown"),
			ASN:         asn,
			ISP:         isp,
		}
	}
	return nil
}

// parseOrg 解析 ipinfo 的 org 字段（形如 "AS13335 Cloudflare, Inc."）
func parseOrg(org string) (asn, isp string) {
	asn, isp = "Unknown", "Unknown"
	if org == "" {
		return
	}
	fields := strings.SplitN(org, " ", 2)
	if strings.HasPrefix(fields[0], "AS") {
		asn = fields[0]
	}
	if len(fields) > 1 {
		isp = fields[1]
	} else {
		isp = fields[0]
	}
	return asn, isp
}

// ==================== 缓存与 token 文件 ====================

func loadTokens(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var tokens []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			tokens = append(tokens, line)
		}
	}
	return tokens
}

func loadIPInfoCache(path string) map[string]string {
	cache := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return cache
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.Contains(line, "#") {
			continue
		}
		ipport, tag, _ := strings.Cut(line, "#")
		cache[strings.TrimSpace(ipport)] = strings.TrimSpace(tag)
	}
	return cache
}

// sortCacheFile 按 IP 排序重写缓存文件（便于人工查看与 diff）
func sortCacheFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	type entry struct {
		ip   net.IP
		line string
	}
	var entries []entry
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || !strings.Contains(line, "#") {
			continue
		}
		ipport, _, _ := strings.Cut(line, "#")
		host, _, _ := strings.Cut(ipport, ":")
		ip := net.ParseIP(host)
		if ip == nil {
			continue
		}
		entries = append(entries, entry{ip: ip, line: line})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return compareIP(entries[i].ip, entries[j].ip) < 0
	})

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(e.line)
		sb.WriteString("\n")
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o644)
}

// compareIP 比较两个 IP 的大小
func compareIP(a, b net.IP) int {
	a4, b4 := a.To4(), b.To4()
	if a4 != nil && b4 != nil {
		for i := 0; i < 4; i++ {
			if a4[i] != b4[i] {
				if a4[i] < b4[i] {
					return -1
				}
				return 1
			}
		}
		return 0
	}
	return strings.Compare(a.String(), b.String())
}

// validateTokens 并发校验 token 有效性
func validateTokens(cfg *Config, tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	logf("正在进行 token 有效性校验...")
	client := newHTTPClient(5*time.Second, 5*time.Second, !cfg.ForceDirect)

	valid := parallelRun(tokens, maxInt(1, cfg.IPCalibrationConcurrency), func(token string) bool {
		body, err := httpGetWithClient(client, "https://ipinfo.io/json?token="+token)
		if err != nil {
			return false
		}
		var data struct {
			Country string `json:"country"`
		}
		return json.Unmarshal(body, &data) == nil && data.Country != ""
	})

	var ok []string
	for i, v := range valid {
		if v {
			ok = append(ok, tokens[i])
		}
	}
	logf("Token 校验完成，有效 %d/%d", len(ok), len(tokens))
	return ok
}

// calibrateRegions 校准节点国家标签，结果写入缓存并复用
func calibrateRegions(cfg *Config, nodes []string, tokenFile, cacheFile string, notifier *Notifier) {
	if !cfg.IPCalibrationEnabled {
		logf("IP 地区校准已禁用，跳过。")
		return
	}

	cache := loadIPInfoCache(cacheFile)
	applyCache(nodes, cache)

	tokens := loadTokens(tokenFile)
	if len(tokens) == 0 {
		logf("%s 为空，IP 地区校准跳过。", tokenFile)
		return
	}

	validTokens := validateTokens(cfg, tokens)
	if len(validTokens) == 0 {
		logf("所有 token 均已失效，地区校准跳过。")
		notifier.Send("IP地区校准：所有token均已失效，本次校准跳过。", "IP校准 Token 耗尽")
		return
	}
	logf("有效 token 数量: %d", len(validTokens))

	// 找出未命中缓存的 ip:port
	newIPPorts := map[string]struct{}{}
	for _, node := range nodes {
		ipport, _, _ := strings.Cut(node, "#")
		if _, hit := cache[ipport]; !hit {
			newIPPorts[ipport] = struct{}{}
		}
	}

	if len(newIPPorts) == 0 {
		logf("所有 IP 已在缓存中，无需查询。")
	} else {
		ipToIPPorts := map[string][]string{}
		var uniqueIPs []string
		for ipport := range newIPPorts {
			ip, _, _ := strings.Cut(ipport, ":")
			if _, seen := ipToIPPorts[ip]; !seen {
				uniqueIPs = append(uniqueIPs, ip)
			}
			ipToIPPorts[ip] = append(ipToIPPorts[ip], ipport)
		}
		logf("检测到 %d 个新 IP:端口，涉及 %d 个唯一 IP，开始查询...", len(newIPPorts), len(uniqueIPs))

		queryNewIPs(cfg, validTokens, uniqueIPs, ipToIPPorts, cache, cacheFile, notifier)
	}

	applyCache(nodes, cache)
	sortCacheFile(cacheFile)
}

// applyCache 将缓存中的国家码回写到节点标签
func applyCache(nodes []string, cache map[string]string) {
	for i, node := range nodes {
		ipport, _, _ := strings.Cut(node, "#")
		if tag, ok := cache[ipport]; ok && tag != "" {
			code := firstField(tag)
			if code == "" {
				continue // 缓存值只有空白：回写会造出 "#" 空标签，不如保持原样
			}
			nodes[i] = ipport + "#" + code
		}
	}
}

// queryNewIPs 并发查询新 IP 并实时写入缓存文件
func queryNewIPs(
	cfg *Config,
	tokens, ips []string,
	ipToIPPorts map[string][]string,
	cache map[string]string,
	cacheFile string,
	notifier *Notifier,
) {
	client := newIPInfoClient(cfg, tokens)

	f, err := os.OpenFile(cacheFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		f = nil
	}
	if f != nil {
		defer f.Close()
	}

	logf("需要查询 %d 个新 IP...", len(ips))
	pp := newProgressPrinter(cfg.ProgressPrintInterval, "🗺️  [地区校准]")

	total := len(ips)
	results, okInputs := parallelRunProgress(ips, maxInt(1, cfg.IPCalibrationConcurrency),
		func(ip string) (*ipDetails, bool) {
			details := client.getDetails(ip)
			return details, details != nil
		}, pp, "")
	failed := total - len(results)

	// results 与 okInputs 顺序一一对应，直接关联写缓存
	for i, d := range results {
		ip := okInputs[i]
		tag := d.buildTag()
		cache[ip] = tag
		for _, ipport := range ipToIPPorts[ip] {
			cache[ipport] = tag
			if f != nil {
				if _, err := fmt.Fprintf(f, "%s#%s\n", ipport, tag); err == nil {
					f.Sync()
				}
			}
		}
	}

	if total > 0 && failed == total {
		notifier.Send("IP地区校准：所有IP查询均失败，可能所有token均已失效。", "IP校准全失败")
		logf("警告：所有 IP 地区校准失败，可能所有 token 已失效。")
	}
}
