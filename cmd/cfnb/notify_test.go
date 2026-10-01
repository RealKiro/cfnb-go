package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWxTokenUnset(t *testing.T) {
	cases := []struct {
		token string
		want  bool
	}{
		{"", true},
		{"   ", true},
		{"your_app_token_here", true},
		{"YOUR_App_Token_Here", true},
		{"AT_1a2b3c4d5e6f77889900aabbccddeeff", false},
		{"  AT_abc123  ", false},
	}
	for _, c := range cases {
		if got := wxTokenUnset(c.token); got != c.want {
			t.Errorf("wxTokenUnset(%q) = %v，期望 %v", c.token, got, c.want)
		}
	}
}

// WxPusher 对无效 appToken 也返回 HTTP 200（body: {"code":1001,...}），
// 只看状态码会把失败误报成「已发送」——这正是真实故障被掩盖的原因
func TestNotifierChecksBusinessCode(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":1001,"msg":"appToken不正确","data":null,"success":false}`))
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.EnableWxPusher = true
	cfg.WxPusherAppToken = "AT_1a2b3c4d5e6f77889900aabbccddeeff"
	cfg.WxPusherAPIURL = srv.URL

	out := captureStdout(t, func() {
		NewNotifier(&cfg).Send("内容", "标题")
	})

	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("期望向 WxPusher 发起 1 次请求，实际 %d 次", n)
	}
	if strings.Contains(out, "微信通知已发送") {
		t.Fatalf("HTTP 200 但业务码为 1001，不应报告成功，实际输出: %s", out)
	}
	if !strings.Contains(out, "发送失败") {
		t.Fatalf("应报告发送失败，实际输出: %s", out)
	}
}

// 业务码为 1000 时才算成功
func TestNotifierReportsSuccessOnOKCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1000,"msg":"处理成功","data":[],"success":true}`))
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.WxPusherAppToken = "AT_1a2b3c4d5e6f77889900aabbccddeeff"
	cfg.WxPusherAPIURL = srv.URL

	out := captureStdout(t, func() {
		NewNotifier(&cfg).Send("内容", "标题")
	})

	if !strings.Contains(out, "微信通知已发送") {
		t.Fatalf("业务码 1000 应报告成功，实际输出: %s", out)
	}
}

// 未配置（占位符）时应直接跳过，不产生任何网络请求
func TestNotifierSkipsWhenUnconfigured(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := defaultConfig()
	cfg.EnableWxPusher = true
	cfg.WxPusherAppToken = "your_app_token_here"
	cfg.WxPusherAPIURL = srv.URL

	out := captureStdout(t, func() {
		NewNotifier(&cfg).Send("内容", "标题")
	})

	if n := atomic.LoadInt32(&hits); n != 0 {
		t.Fatalf("未配置时不应发起请求，实际 %d 次", n)
	}
	if !strings.Contains(out, "未配置") {
		t.Fatalf("应提示未配置，实际输出: %s", out)
	}
}

// 关闭开关时完全静默（不被误认为故障）
func TestNotifierSilentWhenDisabled(t *testing.T) {
	cfg := defaultConfig()
	cfg.EnableWxPusher = false

	out := captureStdout(t, func() {
		NewNotifier(&cfg).Send("内容", "标题")
	})
	if strings.TrimSpace(out) != "" {
		t.Fatalf("关闭通知时不应输出日志，实际输出: %s", out)
	}
}
