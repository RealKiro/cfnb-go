package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
)

// sortBySpeedDesc 按速度降序排序
func sortBySpeedDesc(results []BandwidthResult) {
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Speed > results[j].Speed
	})
}

// sortNodeResults 按成功率降序、延迟升序排序（对应 Python: key=(-success, latency)）
func sortNodeResults(results []*NodeResult) {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Success != results[j].Success {
			return results[i].Success > results[j].Success
		}
		return results[i].Latency < results[j].Latency
	})
}

// writeIPTxt 写出优选结果（支持头部/尾部/行尾广告与指标附加）
func writeIPTxt(
	cfg *Config,
	finalNodes []string,
	speedMap map[string]float64,
	latencyMap map[string]float64,
	httpLatencyMap map[string]float64,
	httpJitterMap map[string]float64,
) error {
	f, err := os.Create(cfg.OutputFile)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	defer w.Flush()

	if cfg.AdHeaderEnabled {
		for _, line := range cfg.AdHeaderLines {
			fmt.Fprintln(w, line)
		}
	}

	for _, node := range finalNodes {
		var sb strings.Builder
		sb.WriteString(node)

		if cfg.IPTxtShowBandwidth && speedMap != nil {
			if v, ok := speedMap[node]; ok {
				sb.WriteString(fmt.Sprintf(" %.2f Mbps", v))
			}
		}
		if cfg.IPTxtShowHTTPLatency && httpLatencyMap != nil {
			if v, ok := httpLatencyMap[node]; ok {
				sb.WriteString(fmt.Sprintf(" %.2f ms", v))
			}
		}
		if cfg.IPTxtShowHTTPJitter && httpJitterMap != nil {
			if v, ok := httpJitterMap[node]; ok {
				sb.WriteString(fmt.Sprintf(" %.2f ms", v))
			}
		}
		if cfg.IPTxtShowLatency && latencyMap != nil {
			if v, ok := latencyMap[node]; ok {
				sb.WriteString(fmt.Sprintf(" %.2f ms", v*1000))
			}
		}
		if cfg.AdPerLineEnable && cfg.AdPerLineText != "" {
			sb.WriteString(cfg.AdPerLineText)
		}

		fmt.Fprintln(w, sb.String())
	}

	if cfg.AdFooterEnabled {
		for _, line := range cfg.AdFooterLines {
			fmt.Fprintln(w, line)
		}
	}

	return nil
}
