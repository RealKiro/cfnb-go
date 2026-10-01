package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ==================== IP 风险等级查询（ipapi.is）====================

var riskLevelOrder = map[string]int{
	"极度纯净": 0,
	"纯净":   1,
	"轻微风险": 2,
	"高风险":  3,
	"极度危险": 4,
}

var riskScorePattern = regexp.MustCompile(`([\d.]+)\s*\(`)

// getIPRiskLevel 查询单个 IP 的风险等级，失败返回 "未知"
func getIPRiskLevel(client *http.Client, ip string) string {
	body, err := httpGetWithClient(client, "https://api.ipapi.is/?q="+url.QueryEscape(ip))
	if err != nil {
		return "未知"
	}

	var data struct {
		Company struct {
			AbuserScore any `json:"abuser_score"`
		} `json:"company"`
		ASN struct {
			AbuserScore any `json:"abuser_score"`
		} `json:"asn"`
		IsCrawler bool `json:"is_crawler"`
		IsProxy   bool `json:"is_proxy"`
		IsVPN     bool `json:"is_vpn"`
		IsTor     bool `json:"is_tor"`
		IsAbuser  bool `json:"is_abuser"`
		IsBogon   bool `json:"is_bogon"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "未知"
	}

	company := extractRiskScore(data.Company.AbuserScore)
	asn := extractRiskScore(data.ASN.AbuserScore)
	baseScore := ((company + asn) / 2) * 5

	flags := []bool{data.IsCrawler, data.IsProxy, data.IsVPN, data.IsTor, data.IsAbuser}
	riskCount := 0
	for _, f := range flags {
		if f {
			riskCount++
		}
	}
	finalScore := baseScore + float64(riskCount)*0.15
	if data.IsBogon {
		finalScore += 1.0
	}

	switch percentage := finalScore * 100; {
	case percentage >= 100:
		return "极度危险"
	case percentage >= 20:
		return "高风险"
	case percentage >= 5:
		return "轻微风险"
	case percentage >= 0.25:
		return "纯净"
	default:
		return "极度纯净"
	}
}

// extractRiskScore 从 "0.5 (详情)" 形式的评分串中提取数值
func extractRiskScore(v any) float64 {
	switch s := v.(type) {
	case nil:
		return 0
	case float64:
		return s
	case string:
		s = strings.TrimSpace(s)
		if s == "" {
			return 0
		}
		if m := riskScorePattern.FindStringSubmatch(s); len(m) == 2 {
			if f, err := strconv.ParseFloat(m[1], 64); err == nil {
				return f
			}
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return 0
}

// ==================== Cloudflare DNS 批量更新 ====================

// batchUpdateCloudflareDNS 将优选结果原子批量更新到 Cloudflare DNS。
// 仅作用于 DNS 环节的过滤：端口(443) → IPv6 落地 → 国家黑名单 → IP 风险等级（带回退）
func batchUpdateCloudflareDNS(
	cfg *Config,
	notifier *Notifier,
	ipList []string,
	stacks map[string]string,
	bwResults []BandwidthResult,
	latencyMap map[string]float64,
	httpLatencyMap map[string]float64,
	httpJitterMap map[string]float64,
) {
	if !cfg.CFEnabled {
		logf("Cloudflare DNS 批量更新未启用。")
		return
	}

	recordType := strings.ToUpper(cfg.DNSRecordType)
	if recordType != "A" && recordType != "TXT" {
		logf("不支持的 DNS_RECORD_TYPE: %s，已跳过 DNS 更新。", recordType)
		return
	}

	targetCount := cfg.DNSUpdateTargetCount
	if targetCount <= 0 {
		targetCount = 15
	}

	var (
		dnsContentList []string
		dnsNodeList    []string
		filteredByPort int
		filteredByIPv6 int
		filteredByCtry int
		filteredByRisk int
		riskFallbackIP []string
		riskFallbackNd []string
	)

	// ---------- 风险等级批量查询 ----------
	riskMap := map[string]string{}
	if cfg.DNSIPRiskFilterEnabled && len(bwResults) > 0 {
		ipSet := map[string]struct{}{}
		for _, r := range bwResults {
			if ip, _, ok := strings.Cut(r.Node, ":"); ok {
				ipSet[ip] = struct{}{}
			}
		}
		if len(ipSet) > 0 {
			ips := make([]string, 0, len(ipSet))
			for ip := range ipSet {
				ips = append(ips, ip)
			}
			workers := cfg.FallbackWorkers
			if workers > len(ips) {
				workers = len(ips)
			}
			logf("正在并发查询 %d 个 IP 的风险等级（并发 %d）...", len(ips), workers)
			client := newHTTPClient(10*time.Second, 10*time.Second, true)
			results := parallelRun(ips, workers, func(ip string) string {
				return getIPRiskLevel(client, ip)
			})
			for i, ip := range ips {
				riskMap[ip] = results[i]
			}
			logf("风险等级查询完成。")
		}
	}

	// ---------- 依据 DNS 规则筛选节点 ----------
	if len(bwResults) > 0 && stacks != nil {
		blockedSet := map[string]struct{}{}
		if cfg.FilterBlockedCountriesEnabled {
			for _, c := range cfg.BlockedCountries {
				blockedSet[strings.ToUpper(c)] = struct{}{}
			}
		}

		for _, r := range bwResults {
			nodeStr := r.Node
			ip, rest, ok := strings.Cut(nodeStr, ":")
			if !ok {
				continue
			}
			port, _, _ := strings.Cut(rest, "#")

			// 端口过滤（A 记录要求 443）
			if recordType == "A" && port != "443" {
				filteredByPort++
				continue
			}

			// IPv6 落地过滤
			if cfg.FilterIPv6Availability {
				if stacks[nodeStr] == "ipv6_only" {
					filteredByIPv6++
					continue
				}
			}

			// 国家黑名单过滤
			if len(blockedSet) > 0 && strings.Contains(nodeStr, "#") {
				tag := nodeStr[strings.LastIndex(nodeStr, "#")+1:]
				country := strings.ToUpper(strings.Fields(tag)[0])
				if _, blocked := blockedSet[country]; blocked {
					filteredByCtry++
					continue
				}
			}

			if cfg.DNSIPRiskFilterEnabled {
				riskFallbackIP = append(riskFallbackIP, ip)
				riskFallbackNd = append(riskFallbackNd, nodeStr)

				level := orDefault(riskMap[ip], "未知")
				if level == "未知" || riskLevelOrder[level] > riskLevelOrder[cfg.DNSIPRiskMaxLevel] {
					filteredByRisk++
					continue
				}
			}

			if recordType == "A" {
				dnsContentList = append(dnsContentList, ip)
			} else {
				dnsContentList = append(dnsContentList, ip+":"+port)
			}
			dnsNodeList = append(dnsNodeList, nodeStr)

			if len(dnsContentList) >= targetCount {
				break
			}
		}

		// 风险过滤全部失败 → 回退到未做风险过滤的列表
		if cfg.DNSIPRiskFilterEnabled && len(dnsContentList) == 0 && filteredByRisk > 0 {
			notifier.Send(
				"风险等级检测全部失败：所有候选节点均因风险等级过高或 API 查询失败被过滤，已回退到无风险等级过滤的候选列表。",
				"风险等级检测全部失败",
			)
			for i := range riskFallbackIP {
				if recordType == "A" {
					dnsContentList = append(dnsContentList, riskFallbackIP[i])
				} else {
					ipPort, _, _ := strings.Cut(riskFallbackNd[i], "#")
					dnsContentList = append(dnsContentList, ipPort)
				}
				dnsNodeList = append(dnsNodeList, riskFallbackNd[i])
				if len(dnsContentList) >= targetCount {
					break
				}
			}
		}

		var parts []string
		if filteredByPort > 0 {
			parts = append(parts, fmt.Sprintf("非443端口过滤(%d个)", filteredByPort))
		}
		if cfg.FilterIPv6Availability {
			parts = append(parts, fmt.Sprintf("IPv6落地过滤(%d个)", filteredByIPv6))
		}
		if cfg.FilterBlockedCountriesEnabled {
			parts = append(parts, fmt.Sprintf("DNS黑名单过滤(%d个)", filteredByCtry))
		}
		if cfg.DNSIPRiskFilterEnabled && filteredByRisk > 0 {
			parts = append(parts, fmt.Sprintf("风险等级过滤(%d个)", filteredByRisk))
		}
		filterStr := "无过滤"
		if len(parts) > 0 {
			filterStr = strings.Join(parts, " + ")
		}
		unit := "IP"
		if recordType == "TXT" {
			unit = "IP:端口"
		}
		logf("从 %d 个测速节点中筛选出 %d 个%s 用于 DNS 更新（%s）。", len(bwResults), len(dnsContentList), unit, filterStr)
	}

	// ---------- 无候选时降级 ----------
	if len(dnsContentList) == 0 {
		if len(ipList) > 0 {
			logf("未能从完整测速结果构建 DNS 列表，降级使用 ip.txt 中的 IP。")
			if recordType == "A" {
				dnsContentList = append(dnsContentList, ipList...)
				dnsNodeList = append(dnsNodeList, ipList...)
			} else {
				logf("TXT 模式需要端口信息，但降级数据中无端口，DNS 更新跳过。")
				return
			}
		} else {
			msg := "没有可用的 IP 用于 DNS 更新，跳过。"
			logf("%s", msg)
			notifier.Send(msg, "DNS 更新跳过")
			return
		}
	}

	// ---------- 去重 ----------
	seen := map[string]struct{}{}
	var uniqueContent, uniqueNodes []string
	for i, content := range dnsContentList {
		if _, dup := seen[content]; dup {
			continue
		}
		seen[content] = struct{}{}
		uniqueContent = append(uniqueContent, content)
		uniqueNodes = append(uniqueNodes, dnsNodeList[i])
	}
	dnsContentList, dnsNodeList = uniqueContent, uniqueNodes

	// ---------- 打印待更新清单 ----------
	unit := "IP"
	if recordType == "TXT" {
		unit = "IP:端口"
	}
	logf("\n准备将以下 %d 个%s 更新到 Cloudflare DNS（记录类型 %s）:", len(dnsContentList), unit, recordType)
	speedMap := make(map[string]float64, len(bwResults))
	for _, r := range bwResults {
		speedMap[r.Node] = r.Speed
	}
	for i, content := range dnsContentList {
		node := dnsNodeList[i]
		display := content
		if strings.Contains(node, "#") {
			display = node
		}
		line := fmt.Sprintf("%d. %s 速度 %.2f Mbps", i+1, display, speedMap[node])
		if v, ok := httpLatencyMap[node]; ok {
			line += fmt.Sprintf(" 延迟 %.2f ms", v)
		}
		if v, ok := httpJitterMap[node]; ok {
			line += fmt.Sprintf(" 抖动 %.2f ms", v)
		}
		if v, ok := latencyMap[node]; ok {
			line += fmt.Sprintf(" 延迟 %.2f ms", v*1000)
		}
		logf("%s", line)
	}

	// ---------- 提交更新 ----------
	client := newHTTPClient(
		time.Duration(cfg.CFDNSConnectTimout)*time.Second,
		time.Duration(cfg.CFDNSReadTimeout)*time.Second,
		true,
	)
	for attempt := 1; attempt <= cfg.DNSUpdateMaxRetries; attempt++ {
		logf("\n[DNS 更新] 尝试 %d/%d...", attempt, cfg.DNSUpdateMaxRetries)
		err := submitDNSRecords(cfg, client, recordType, dnsContentList)
		if err == nil {
			if recordType == "A" {
				logf("Cloudflare DNS 批量更新成功！已将 %s 指向 %d 个 IP。", cfg.CFDNSRecordName, len(dnsContentList))
			} else {
				logf("Cloudflare TXT 记录批量更新成功！共 %d 条记录，每条内容为一个 IP:端口。", len(dnsContentList))
			}
			return
		}
		logf("[尝试 %d/%d] DNS 更新出错: %v", attempt, cfg.DNSUpdateMaxRetries, err)
		if attempt < cfg.DNSUpdateMaxRetries {
			sleepSeconds(cfg.DNSUpdateRetryDelay)
		} else {
			msg := fmt.Sprintf("Cloudflare DNS 更新失败，已重试 %d 次，错误：%v", cfg.DNSUpdateMaxRetries, err)
			logf("%s", msg)
			notifier.Send(msg, "DNS 更新失败")
		}
	}
}

// submitDNSRecords 单次原子批量更新（删除全部旧记录 + 创建新记录）
func submitDNSRecords(cfg *Config, client *http.Client, recordType string, contents []string) error {
	headers := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+cfg.CFAPIToken)
		req.Header.Set("Content-Type", "application/json")
	}

	listURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=%s&name=%s",
		cfg.CFZoneID, recordType, url.QueryEscape(cfg.CFDNSRecordName))

	req, err := http.NewRequest(http.MethodGet, listURL, nil)
	if err != nil {
		return err
	}
	headers(req)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	listBody, _ := readAllLimit(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("查询 DNS 记录失败 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(listBody)))
	}

	var listResult struct {
		Success bool `json:"success"`
		Errors  any  `json:"errors"`
		Result  []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(listBody, &listResult); err != nil {
		return err
	}
	if !listResult.Success {
		return fmt.Errorf("查询 DNS 记录失败: %v", listResult.Errors)
	}

	deletes := make([]map[string]string, 0, len(listResult.Result))
	for _, rec := range listResult.Result {
		deletes = append(deletes, map[string]string{"id": rec.ID})
	}

	posts := make([]map[string]any, 0, len(contents))
	for _, content := range contents {
		post := map[string]any{
			"name":    cfg.CFDNSRecordName,
			"type":    recordType,
			"content": content,
			"ttl":     cfg.CFTTL,
		}
		if recordType == "A" {
			post["proxied"] = cfg.CFProxied
		}
		posts = append(posts, post)
	}

	payload, err := json.Marshal(map[string]any{"deletes": deletes, "posts": posts})
	if err != nil {
		return err
	}

	batchURL := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/batch", cfg.CFZoneID)
	batchReq, err := http.NewRequest(http.MethodPost, batchURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	headers(batchReq)

	batchResp, err := client.Do(batchReq)
	if err != nil {
		return err
	}
	batchBody, _ := readAllLimit(batchResp.Body)
	batchResp.Body.Close()

	var batchResult struct {
		Success bool `json:"success"`
		Errors  any  `json:"errors"`
	}
	if err := json.Unmarshal(batchBody, &batchResult); err != nil {
		return fmt.Errorf("HTTP %d: %s", batchResp.StatusCode, strings.TrimSpace(string(batchBody)))
	}
	if !batchResult.Success {
		return fmt.Errorf("批量更新失败: %v", batchResult.Errors)
	}
	return nil
}

// sortStrings 便于测试与展示的稳定排序
func sortStrings(items []string) []string {
	out := append([]string(nil), items...)
	sort.Strings(out)
	return out
}
