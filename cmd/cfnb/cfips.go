package main

import (
	"net"
	"net/netip"
)

// ==================== Cloudflare 官方网段（AS13335）校验 ====================
//
// 优选目标必须是 Cloudflare 官方 anycast IP：第三方反代机即使 TCP 可达、
// 测速全绿，其 SNI 行为（是否透传自定义域名）也不受控制——实测反代接受
// TCP 连接后对自定义域名的 ClientHello 静默丢弃，业务域名直接超时。
//
// 校验用本地网段匹配实现（Cloudflare 官方公布的全部 IPv4 网段，等价于
// AS13335 的对外空间），不依赖任何外部 API：无 token、无限速、确定性返回。

// cloudflareIPv4CIDRs Cloudflare 官方公布的全部 IPv4 网段。
// 来源：https://www.cloudflare.com/ips-v4（2026-10 校对）。官方极少变动，
// 若未来新增网段，更新此表即可。
var cloudflareIPv4CIDRs = []string{
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
}

// cloudflareIPSet 包初始化时解析好的网段匹配表
var cloudflareIPSet = func() []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(cloudflareIPv4CIDRs))
	for _, cidr := range cloudflareIPv4CIDRs {
		prefixes = append(prefixes, netip.MustParsePrefix(cidr))
	}
	return prefixes
}()

// isCloudflareIP 判断 IP 是否属于 Cloudflare 官方公布的 IPv4 网段。
// 非 IPv4 输入（含空串、域名、IPv6）一律返回 false。
func isCloudflareIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	addr, ok := netip.AddrFromSlice(ip.To4())
	if !ok {
		return false
	}
	for _, prefix := range cloudflareIPSet {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
