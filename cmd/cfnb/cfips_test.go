package main

import "testing"

func TestIsCloudflareIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"104.16.132.229", true},   // 104.16.0.0/13
		{"104.24.10.10", true},     // 104.24.0.0/14
		{"172.64.1.1", true},       // 172.64.0.0/13
		{"198.41.200.1", true},     // 198.41.128.0/17
		{"162.158.255.254", true},  // 162.158.0.0/15
		{"20.212.51.155", false},   // Azure（实测踩坑：第三方反代 IP）
		{"143.198.211.180", false}, // DigitalOcean
		{"161.118.220.61", false},  // Oracle
		{"84.32.22.205", false},    // 欧洲 VPS
		{"8.8.8.8", false},
		{"192.168.1.1", false}, // 私网
		{"", false},
		{"not-an-ip", false},
		{"2606:4700::1", false}, // IPv6 不在本表
	}
	for _, tt := range tests {
		if got := isCloudflareIP(tt.ip); got != tt.want {
			t.Errorf("isCloudflareIP(%q) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}
