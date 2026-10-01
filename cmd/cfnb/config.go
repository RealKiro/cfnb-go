package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SourceConfig 数据源配置
type SourceConfig struct {
	URL     string `json:"url"`
	Enabled *bool  `json:"enabled"`
}

func (s SourceConfig) IsEnabled() bool {
	return s.Enabled == nil || *s.Enabled
}

// Config 与 Python 版 config.json 字段完全兼容，另新增 GitHub API 同步字段。
// 加载方式：先构造带默认值的结构体，再用 json.Unmarshal 覆盖用户配置中出现的字段，
// 因此未配置项自动保持默认值（无需逐字段判断是否缺失）。
type Config struct {
	// ---------- 筛选模式与数量 ----------
	UseGlobalMode     bool `json:"USE_GLOBAL_MODE"`
	GlobalTopN        int  `json:"GLOBAL_TOP_N"`
	PerCountryTopN    int  `json:"PER_COUNTRY_TOP_N"`
	BandwidthCandidat int  `json:"BANDWIDTH_CANDIDATES"`

	// ---------- TCP 测试 ----------
	TCPProbes             int     `json:"TCP_PROBES"`
	MinSuccessRate        float64 `json:"MIN_SUCCESS_RATE"`
	TCPLatencyWeight      float64 `json:"TCP_LATENCY_WEIGHT"`
	Timeout               float64 `json:"TIMEOUT"`
	SocketDefaultTimeout  int     `json:"SOCKET_DEFAULT_TIMEOUT"`
	ProgressPrintInterval float64 `json:"PROGRESS_PRINT_INTERVAL"`

	// ---------- 前置过滤 ----------
	FilterCountriesEnabled    bool     `json:"FILTER_COUNTRIES_ENABLED"`
	AllowedCountries          []string `json:"ALLOWED_COUNTRIES"`
	PreFilterBlockedEnabled   bool     `json:"PRE_FILTER_BLOCKED_ENABLED"`
	PreFilterBlockedCountries []string `json:"PRE_FILTER_BLOCKED_COUNTRIES"`
	PreFilterUseDNSBlocklist  bool     `json:"PRE_FILTER_USE_DNS_BLOCKLIST"`
	PreFilterPortEnabled      bool     `json:"PRE_FILTER_PORT_ENABLED"`
	PreFilterPorts            []int    `json:"PRE_FILTER_PORTS"`

	// ---------- 测速前 IPv6 落地过滤 ----------
	// 开启后在可用性检测之后、HTTP 与带宽测速之前剔除 inferred_stack=ipv6_only
	// 的节点：既省下最耗时的带宽测速，也让 ip.txt 只留 IPv4 可用节点。
	PreBandwidthIPv6FilterEnabled bool `json:"PRE_BANDWIDTH_IPV6_FILTER_ENABLED"`

	// ---------- 测速前 抖动 过滤 ----------
	// 开启后在 HTTP 检测之后、带宽测速之前剔除「HTTP 抖动超标」的节点。
	// 抖动是同一节点多次探测延迟的标准差（毫秒）。它在候选之间的区分度远大于
	// TCP 延迟（实测极差 92.7 倍 vs 1.4 倍），且抖动大的节点在 penalty 里还会被
	// HTTP 延迟项再罚一次（两者相关系数约 +1）——与其让它占用最耗时的带宽测速
	// 名额，不如提前筛掉。
	// 默认关闭：抖动是单轮量、波动很大（实测同一 IP 相邻两轮 1.09 / 13.86 ms），
	// 贸然开启容易误杀。
	PreBandwidthMaxJitterEnabled bool    `json:"PRE_BANDWIDTH_MAX_JITTER_ENABLED"`
	PreBandwidthMaxJitterMs      float64 `json:"PRE_BANDWIDTH_MAX_JITTER_MS"`

	// ---------- 微信通知 ----------
	EnableWxPusher      bool     `json:"ENABLE_WXPUSHER"`
	WxPusherAppToken    string   `json:"WXPUSHER_APP_TOKEN"`
	WxPusherUIDs        []string `json:"WXPUSHER_UIDS"`
	WxPusherAPIURL      string   `json:"WXPUSHER_API_URL"`
	NotifyTimeout       int      `json:"NOTIFY_TIMEOUT"`
	NotifyConnectTimout int      `json:"NOTIFY_CONNECT_TIMEOUT"`

	// ---------- Cloudflare DNS ----------
	CFEnabled          bool   `json:"CF_ENABLED"`
	CFAPIToken         string `json:"CF_API_TOKEN"`
	CFZoneID           string `json:"CF_ZONE_ID"`
	CFDNSRecordName    string `json:"CF_DNS_RECORD_NAME"`
	CFTTL              int    `json:"CF_TTL"`
	CFProxied          bool   `json:"CF_PROXIED"`
	CFDNSConnectTimout int    `json:"CF_DNS_CONNECT_TIMEOUT"`
	CFDNSReadTimeout   int    `json:"CF_DNS_READ_TIMEOUT"`
	DNSRecordType      string `json:"DNS_RECORD_TYPE"`

	// ---------- 数据源 ----------
	AdditionalSources  []SourceConfig `json:"ADDITIONAL_SOURCES"`
	BareIPDefaultPort  int            `json:"BARE_IP_DEFAULT_PORT"`
	KeepUnlabeledNodes bool           `json:"KEEP_UNLABELED_NODES"`
	FetchMaxRetries    int            `json:"FETCH_MAX_RETRIES"`
	FetchRetryDelay    int            `json:"FETCH_RETRY_DELAY"`
	FetchTimeout       int            `json:"FETCH_TIMEOUT"`
	FetchConnectTimout int            `json:"FETCH_CONNECT_TIMEOUT"`

	// ---------- IP 地区校准 ----------
	IPCalibrationEnabled     bool    `json:"IP_CALIBRATION_ENABLED"`
	TokenFailureThreshold    float64 `json:"TOKEN_FAILURE_THRESHOLD"`
	IPCalibrationMinInterval float64 `json:"IP_CALIBRATION_MIN_INTERVAL"`
	IPCalibrationTokenFile   string  `json:"IP_CALIBRATION_TOKEN_FILE"`
	IPCalibrationCacheFile   string  `json:"IP_CALIBRATION_CACHE_FILE"`
	IPCalibrationConcurrency int     `json:"IP_CALIBRATION_CONCURRENCY"`

	// ---------- 输出与日志 ----------
	OutputFile    string `json:"OUTPUT_FILE"`
	EnableLogging bool   `json:"ENABLE_LOGGING"`
	LogFile       string `json:"LOG_FILE"`
	ForceDirect   bool   `json:"FORCE_DIRECT"`

	// ---------- 可用性检测 ----------
	TestAvailability            bool   `json:"TEST_AVAILABILITY"`
	AvailabilityCheckAPI        string `json:"AVAILABILITY_CHECK_API"`
	AvailabilityTimeout         int    `json:"AVAILABILITY_TIMEOUT"`
	AvailabilityConnectTimout   int    `json:"AVAILABILITY_CONNECT_TIMEOUT"`
	AvailabilityRetryMax        int    `json:"AVAILABILITY_RETRY_MAX"`
	AvailabilityRetryDelay      int    `json:"AVAILABILITY_RETRY_DELAY"`
	AvailabilityInnerRetry      bool   `json:"AVAILABILITY_INNER_RETRY_ENABLED"`
	AvailabilityInnerRetryMax   int    `json:"AVAILABILITY_INNER_RETRY_MAX"`
	AvailabilityInnerRetryDelay int    `json:"AVAILABILITY_INNER_RETRY_DELAY"`
	AvailabilityWorkers         int    `json:"AVAILABILITY_WORKERS"`
	FallbackWorkers             int    `json:"FALLBACK_WORKERS"`

	// ---------- HTTP 检测 ----------
	HTTPTestEnabled        bool    `json:"HTTP_TEST_ENABLED"`
	HTTPTestTimeout        int     `json:"HTTP_TEST_TIMEOUT"`
	HTTPTestConnectTimeout int     `json:"HTTP_TEST_CONNECT_TIMEOUT"`
	HTTPTestMaxRounds      int     `json:"HTTP_TEST_MAX_ROUNDS"`
	HTTPTestRoundDelay     int     `json:"HTTP_TEST_ROUND_DELAY"`
	HTTPTestInnerRetry     bool    `json:"HTTP_TEST_INNER_RETRY_ENABLED"`
	HTTPTestMaxRetries     int     `json:"HTTP_TEST_MAX_RETRIES"`
	HTTPTestRetryDelay     int     `json:"HTTP_TEST_RETRY_DELAY"`
	HTTPTestMethod         string  `json:"HTTP_TEST_METHOD"`
	HTTPLatencyWeight      float64 `json:"HTTP_LATENCY_WEIGHT"`
	JitterWeight           float64 `json:"JITTER_WEIGHT"`
	HTTPJitterSamples      int     `json:"HTTP_JITTER_SAMPLES"`
	HTTPTestWorkers        int     `json:"HTTP_TEST_WORKERS"`

	// ---------- DNS 更新过滤 ----------
	FilterIPv6Availability        bool     `json:"FILTER_IPV6_AVAILABILITY"`
	FilterBlockedCountriesEnabled bool     `json:"FILTER_BLOCKED_COUNTRIES_ENABLED"`
	BlockedCountries              []string `json:"BLOCKED_COUNTRIES"`
	DNSIPRiskFilterEnabled        bool     `json:"DNS_IP_RISK_FILTER_ENABLED"`
	DNSIPRiskMaxLevel             string   `json:"DNS_IP_RISK_MAX_LEVEL"`
	DNSUpdateTargetCount          int      `json:"DNS_UPDATE_TARGET_COUNT"`

	// ---------- 带宽测速 ----------
	BandwidthSizeMB      float64 `json:"BANDWIDTH_SIZE_MB"`
	BandwidthTimeout     int     `json:"BANDWIDTH_TIMEOUT"`
	BandwidthRetryMax    int     `json:"BANDWIDTH_RETRY_MAX"`
	BandwidthRetryDelay  int     `json:"BANDWIDTH_RETRY_DELAY"`
	BandwidthURLTemplate string  `json:"BANDWIDTH_URL_TEMPLATE"`
	BandwidthProcessBuf  int     `json:"BANDWIDTH_PROCESS_BUFFER"`
	BandwidthConnectTO   int     `json:"BANDWIDTH_CONNECT_TIMEOUT"`
	SpeedWeight          float64 `json:"SPEED_WEIGHT"`
	BandwidthWorkers     int     `json:"BANDWIDTH_WORKERS"`
	MaxWorkers           int     `json:"MAX_WORKERS"`

	// ---------- 重试策略 ----------
	DNSUpdateMaxRetries  int `json:"DNS_UPDATE_MAX_RETRIES"`
	DNSUpdateRetryDelay  int `json:"DNS_UPDATE_RETRY_DELAY"`
	GitHubSyncMaxRetries int `json:"GITHUB_SYNC_MAX_RETRIES"`
	GitHubSyncRetryDelay int `json:"GITHUB_SYNC_RETRY_DELAY"`
	GitSyncProcessTimout int `json:"GIT_SYNC_PROCESS_TIMEOUT"`

	// ---------- 广告植入 ----------
	AdHeaderEnabled bool     `json:"AD_HEADER_ENABLED"`
	AdHeaderLines   []string `json:"AD_HEADER_LINES"`
	AdFooterEnabled bool     `json:"AD_FOOTER_ENABLED"`
	AdFooterLines   []string `json:"AD_FOOTER_LINES"`
	AdPerLineEnable bool     `json:"AD_PERLINE_ENABLED"`
	AdPerLineText   string   `json:"AD_PERLINE_TEXT"`

	// ---------- ip.txt 指标输出 ----------
	IPTxtShowBandwidth   bool `json:"IP_TXT_SHOW_BANDWIDTH"`
	IPTxtShowHTTPLatency bool `json:"IP_TXT_SHOW_HTTP_LATENCY"`
	IPTxtShowHTTPJitter  bool `json:"IP_TXT_SHOW_HTTP_JITTER"`
	IPTxtShowLatency     bool `json:"IP_TXT_SHOW_LATENCY"`

	// ---------- GitHub 同步（Go 版新增：使用 Contents API，无需 git 与脚本）----------
	GitHubToken    string `json:"GITHUB_TOKEN"`
	GitHubOwner    string `json:"GITHUB_OWNER"`
	GitHubRepo     string `json:"GITHUB_REPO"`
	GitHubBranch   string `json:"GITHUB_BRANCH"`
	GitHubSyncPath string `json:"GITHUB_SYNC_PATH"`
	GitHubAPIBase  string `json:"GITHUB_API_BASE"`
}

// defaultConfig 返回内置默认值（与 Python 版 defaults 一致）
func defaultConfig() Config {
	return Config{
		UseGlobalMode:     true,
		GlobalTopN:        15,
		PerCountryTopN:    1,
		BandwidthCandidat: 300,

		TCPProbes:             1,
		MinSuccessRate:        1.0,
		TCPLatencyWeight:      0.0,
		Timeout:               2.0,
		SocketDefaultTimeout:  3,
		ProgressPrintInterval: 1,

		FilterCountriesEnabled:    false,
		AllowedCountries:          []string{"US"},
		PreFilterBlockedEnabled:   true,
		PreFilterBlockedCountries: []string{"CN"},
		PreFilterUseDNSBlocklist:  true,
		PreFilterPortEnabled:      true,
		PreFilterPorts:            []int{443},

		PreBandwidthIPv6FilterEnabled: true,

		PreBandwidthMaxJitterEnabled: false,
		PreBandwidthMaxJitterMs:      50.0,

		EnableWxPusher:      true,
		WxPusherAppToken:    "your_app_token_here",
		WxPusherUIDs:        []string{"your_uid_here"},
		WxPusherAPIURL:      "https://wxpusher.zjiecode.com/api/send/message",
		NotifyTimeout:       3,
		NotifyConnectTimout: 3,

		CFEnabled:          true,
		CFAPIToken:         "your_CF_API_TOKEN",
		CFZoneID:           "your_CF_ZONE_ID",
		CFDNSRecordName:    "your_CF_DNS_RECORD_NAME",
		CFTTL:              60,
		CFProxied:          false,
		CFDNSConnectTimout: 3,
		CFDNSReadTimeout:   3,
		DNSRecordType:      "TXT",

		AdditionalSources: []SourceConfig{
			{URL: "https://zip.cm.edu.kg/all.txt"},
			{URL: "https://countrymerge.pages.dev/all.txt"},
			{URL: "https://ipdb.api.030101.xyz/?type=bestproxy&country=true"},
			// GitHub 社区维护的高频更新源。
			// 经 ghproxy.net 中转：raw.githubusercontent.com 在国内（尤其容器内）
			// 常被 RST，三次重试也可能全废；实测该镜像返回内容与原始源逐字节一致。
			// 注意别用 cdn.jsdelivr.net——它按分支缓存，会拿到明显过期的旧榜单。
			{URL: "https://ghproxy.net/https://raw.githubusercontent.com/yuanxiawan/cfipv4db/refs/heads/main/high_score_ips.txt"},
			{URL: "https://ghproxy.net/https://raw.githubusercontent.com/cmliu/WorkerVless2sub/refs/heads/main/addressesapi.txt"},
			// 社区优选域名：非 http(s) 写法 → 按域名做 DNS 解析，取全部 A 记录
			{URL: "cf.090227.xyz"},
			{URL: "cmcc.090227.xyz"},
		},
		// 该 API 只吐裸 IP（无端口），统一补 443；设 0 可关闭此补全行为
		BareIPDefaultPort:  443,
		KeepUnlabeledNodes: true,
		FetchMaxRetries:    3,
		FetchRetryDelay:    3,
		FetchTimeout:       3,
		FetchConnectTimout: 3,

		IPCalibrationEnabled:     false,
		TokenFailureThreshold:    3,
		IPCalibrationMinInterval: 0.1,
		IPCalibrationTokenFile:   "valid_tokens.txt",
		IPCalibrationCacheFile:   "ipinfo_cache.txt",
		IPCalibrationConcurrency: 300,

		OutputFile:    "ip.txt",
		EnableLogging: false,
		LogFile:       "cfnb.log",
		ForceDirect:   false,

		TestAvailability:            true,
		AvailabilityCheckAPI:        "https://api.090227.xyz/check",
		AvailabilityTimeout:         3,
		AvailabilityConnectTimout:   3,
		AvailabilityRetryMax:        2,
		AvailabilityRetryDelay:      3,
		AvailabilityInnerRetry:      true,
		AvailabilityInnerRetryMax:   2,
		AvailabilityInnerRetryDelay: 3,
		AvailabilityWorkers:         32,
		FallbackWorkers:             32,

		HTTPTestEnabled:        true,
		HTTPTestTimeout:        3,
		HTTPTestConnectTimeout: 3,
		HTTPTestMaxRounds:      2,
		HTTPTestRoundDelay:     3,
		HTTPTestInnerRetry:     true,
		HTTPTestMaxRetries:     2,
		HTTPTestRetryDelay:     3,
		HTTPTestMethod:         "HEAD",
		HTTPLatencyWeight:      3.0,
		JitterWeight:           3.0,
		HTTPJitterSamples:      3,
		HTTPTestWorkers:        32,

		FilterIPv6Availability:        true,
		FilterBlockedCountriesEnabled: true,
		BlockedCountries: []string{
			"BD", "BI", "BY", "CD", "CF", "CN", "CU", "DE", "ET", "HK",
			"IR", "KP", "LY", "MO", "NG", "NL", "PK", "RU", "SD", "SO",
			"SY", "TH", "TW", "UA", "VE", "VN", "YE", "ZW",
		},
		DNSIPRiskFilterEnabled: false,
		DNSIPRiskMaxLevel:      "高风险",
		DNSUpdateTargetCount:   15,

		BandwidthSizeMB:      1.0,
		BandwidthTimeout:     3,
		BandwidthRetryMax:    2,
		BandwidthRetryDelay:  3,
		BandwidthURLTemplate: "{scheme}://speed.cloudflare.com:{port}/__down?bytes={bytes}",
		BandwidthProcessBuf:  2,
		BandwidthConnectTO:   3,
		SpeedWeight:          3.0,
		BandwidthWorkers:     3,
		MaxWorkers:           300,

		DNSUpdateMaxRetries:  3,
		DNSUpdateRetryDelay:  3,
		GitHubSyncMaxRetries: 3,
		GitHubSyncRetryDelay: 3,
		GitSyncProcessTimout: 180,

		AdHeaderEnabled: false,
		AdHeaderLines:   []string{},
		AdFooterEnabled: false,
		AdFooterLines:   []string{},
		AdPerLineEnable: false,
		AdPerLineText:   "",

		IPTxtShowBandwidth:   false,
		IPTxtShowHTTPLatency: false,
		IPTxtShowHTTPJitter:  false,
		IPTxtShowLatency:     false,

		GitHubBranch:   "main",
		GitHubSyncPath: "ip.txt",
		GitHubAPIBase:  "https://api.github.com",
	}
}

// exeDir 返回可执行文件所在目录（用于定位 config.json 等文件）
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// LoadConfig 读取 config.json；文件缺失时使用内置默认值
func LoadConfig(path string) (Config, error) {
	cfg := defaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("未找到配置文件 %s，将使用内置默认配置运行。\n", path)
			fmt.Println("你可根据需要创建 config.json 文件（参考文档），程序会自动识别。")
			return cfg, nil
		}
		return cfg, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("配置文件格式不正确: %w", err)
	}
	return cfg, nil
}
