package main

import (
	"bytes"
	"encoding/json"
	"net/http"
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

// Send 发送微信通知；未启用时静默跳过
func (n *Notifier) Send(content, summary string) {
	if !n.cfg.EnableWxPusher {
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

	if resp.StatusCode == http.StatusOK {
		logf("微信通知已发送")
	} else {
		logf("微信通知发送失败: %d", resp.StatusCode)
	}
}
