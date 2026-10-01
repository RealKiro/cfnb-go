package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ==================== GitHub 自动同步（Contents API，无需 git 与外部脚本）====================

type githubContentInfo struct {
	SHA string `json:"sha"`
}

// syncToGitHub 将 ip.txt 提交到指定仓库分支。
// 与 Python 版通过 git_sync.sh 强推不同，这里使用 GitHub Contents API：
//   - 无需镜像内安装 git、无需挂载推送脚本
//   - 令牌、仓库、分支等信息统一放在 config.json
//   - 设置 GITHUB_SYNC_MAX_RETRIES = 0 可关闭
func syncToGitHub(cfg *Config, notifier *Notifier) {
	if cfg.GitHubSyncMaxRetries <= 0 {
		logf("GitHub 同步未启用（GITHUB_SYNC_MAX_RETRIES = 0）。")
		return
	}
	if cfg.GitHubToken == "" || cfg.GitHubOwner == "" || cfg.GitHubRepo == "" {
		logf("GitHub 同步信息不完整（需 GITHUB_TOKEN / GITHUB_OWNER / GITHUB_REPO），跳过。")
		return
	}

	content, err := os.ReadFile(cfg.OutputFile)
	if err != nil {
		logf("读取 %s 失败，跳过 GitHub 同步: %v", cfg.OutputFile, err)
		notifier.Send(fmt.Sprintf("GitHub 同步失败：无法读取 %s。", cfg.OutputFile), "GitHub 同步失败")
		return
	}

	branch := orDefault(cfg.GitHubBranch, "main")
	path := orDefault(cfg.GitHubSyncPath, cfg.OutputFile)
	apiBase := strings.TrimRight(orDefault(cfg.GitHubAPIBase, "https://api.github.com"), "/")

	client := newHTTPClient(
		time.Duration(cfg.FetchConnectTimout)*time.Second,
		time.Duration(cfg.GitSyncProcessTimout)*time.Second,
		true,
	)

	var lastErr error
	for attempt := 1; attempt <= cfg.GitHubSyncMaxRetries; attempt++ {
		logf("\n正在同步到 GitHub (尝试 %d/%d)...", attempt, cfg.GitHubSyncMaxRetries)
		lastErr = pushFileToGitHub(cfg, client, apiBase, path, branch, content)
		if lastErr == nil {
			logf("✅ 已自动推送到 GitHub。")
			return
		}
		logf("推送失败: %v", lastErr)
		if attempt < cfg.GitHubSyncMaxRetries {
			sleepSeconds(cfg.GitHubSyncRetryDelay)
		}
	}

	notifier.Send(
		fmt.Sprintf("GitHub 推送失败，已重试 %d 次，错误：%v", cfg.GitHubSyncMaxRetries, lastErr),
		"GitHub 推送失败",
	)
	logf("已尝试 %d 次推送，均失败，请检查网络或 GitHub 仓库状态。", cfg.GitHubSyncMaxRetries)
}

func pushFileToGitHub(cfg *Config, client *http.Client, apiBase, path, branch string, content []byte) error {
	fileURL := fmt.Sprintf("%s/repos/%s/%s/contents/%s",
		apiBase, cfg.GitHubOwner, cfg.GitHubRepo, url.PathEscape(path))

	// 1. 查询现有文件 sha（不存在则为新建）
	var sha string
	getReq, err := http.NewRequest(http.MethodGet, fileURL+"?ref="+url.QueryEscape(branch), nil)
	if err != nil {
		return err
	}
	applyGitHubHeaders(getReq, cfg)
	resp, err := client.Do(getReq)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var info githubContentInfo
		if json.Unmarshal(body, &info) == nil {
			sha = info.SHA
		}
	case http.StatusNotFound:
		// 文件尚不存在，无需 sha
	default:
		return fmt.Errorf("查询文件失败 HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// 2. 提交内容
	payload := map[string]string{
		"message": fmt.Sprintf("Update %s on %s", path, time.Now().Format("2006-01-02 15:04:05")),
		"content": base64.StdEncoding.EncodeToString(content),
		"branch":  branch,
	}
	if sha != "" {
		payload["sha"] = sha
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	putReq, err := http.NewRequest(http.MethodPut, fileURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	applyGitHubHeaders(putReq, cfg)
	putReq.Header.Set("Content-Type", "application/json")

	putResp, err := client.Do(putReq)
	if err != nil {
		return err
	}
	defer putResp.Body.Close()
	putBody, _ := io.ReadAll(io.LimitReader(putResp.Body, 1<<20))

	if putResp.StatusCode != http.StatusOK && putResp.StatusCode != http.StatusCreated {
		return fmt.Errorf("HTTP %d: %s", putResp.StatusCode, strings.TrimSpace(string(putBody)))
	}

	var result struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if json.Unmarshal(putBody, &result) == nil && result.Commit.SHA != "" {
		logf("提交成功，commit: %s", result.Commit.SHA[:minInt(7, len(result.Commit.SHA))])
	}
	return nil
}

func applyGitHubHeaders(req *http.Request, cfg *Config) {
	req.Header.Set("Authorization", "Bearer "+cfg.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cfnb-go")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
