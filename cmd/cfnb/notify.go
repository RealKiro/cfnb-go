package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// Notifier 微信通知（WxPusher）
type Notifier struct {
	cfg    *Config
	client *http.Client
}

func NewNotifier(cfg *Config) *Notifier {
	return &Notifier{
		cfg: cfg,
		client: newHTTPClient(
			time.Duration(cfg.NotifyConnectTimout)*time.Second,
			time.Duration(cfg.NotifyTimeout)*time.Second,
			true,
		),
	}
}

// wxPusherOKCode WxPusher 的业务成功码
const wxPusherOKCode = 1000

// wxResp WxPusher 的响应信封：HTTP 200 不代表发送成功，真正的结果在 code 里
type wxResp struct {
	Code    int    `json:"code"`
	Msg     string `json:"msg"`
	Success bool   `json:"success"`
}

// wxTokenUnset 判定 appToken 是否仍未配置（空值，或模板里的 your_xxx 占位符）
func wxTokenUnset(token string) bool {
	t := strings.TrimSpace(strings.ToLower(token))
	return t == "" || strings.HasPrefix(t, "your_")
}

// Send 发送微信通知；未启用或未配置时跳过
func (n *Notifier) Send(content, summary string) {
	if !n.cfg.EnableWxPusher {
		return
	}

	// 未配置时提前返回。否则会拿着占位符去请求，而 WxPusher 对无效 appToken
	// 依然返回 HTTP 200（实测 body 为 {"code":1001,"msg":"appToken不正确"}），
	// 只看状态码会把「通知根本没发出去」误报成「已发送」，把真实故障一起掩盖掉
	if wxTokenUnset(n.cfg.WxPusherAppToken) {
		logf("微信通知未配置（WXPUSHER_APP_TOKEN 仍是占位符），本轮跳过发送。")
		return
	}

	payload := map[string]any{
		"appToken": n.cfg.WxPusherAppToken,
		"content":  content,
		"summary":  summary,
		"uids":     n.cfg.WxPusherUIDs,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		logf("微信通知异常: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, n.cfg.WxPusherAPIURL, bytes.NewReader(body))
	if err != nil {
		logf("微信通知异常: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := n.client.Do(req)
	if err != nil {
		logf("微信通知异常: %v", err)
		return
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		logf("微信通知发送失败: HTTP %d %s", resp.StatusCode, bodyPreview(raw))
		return
	}

	var r wxResp
	if err := json.Unmarshal(raw, &r); err != nil {
		logf("微信通知响应无法解析: %v（%s）", err, bodyPreview(raw))
		return
	}
	if r.Code != wxPusherOKCode {
		logf("微信通知发送失败: code=%d msg=%s", r.Code, r.Msg)
		return
	}
	logf("微信通知已发送")
}
