package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	DefaultOpenAIBaseURL = "https://api.openai.com/v1"
	DefaultClaudeBaseURL = "https://api.anthropic.com"
	DefaultOllamaBaseURL = "http://localhost:11434/v1"

	catalogTimeout = 15 * time.Second
	maxCatalogSize = 4 << 20
)

type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body)
}

// ListModels asks the provider which models the key can use. Ollama speaks
// the OpenAI-compatible /models too.
func ListModels(ctx context.Context, provider, baseURL, apiKey string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	var url string
	switch provider {
	case "openai":
		if base == "" {
			base = DefaultOpenAIBaseURL
		}
		url = base + "/models"
	case "ollama":
		if base == "" {
			base = DefaultOllamaBaseURL
		}
		url = base + "/models"
	case "claude":
		if base == "" {
			base = DefaultClaudeBaseURL
		}
		url = base + "/v1/models?limit=1000"
	default:
		return nil, fmt.Errorf("unsupported model provider: %s", provider)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		if provider == "claude" {
			req.Header.Set("x-api-key", apiKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	if provider == "claude" {
		req.Header.Set("anthropic-version", "2023-06-01")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogSize))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return nil, &StatusError{Code: resp.StatusCode, Body: snippet}
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unexpected model list response: %w", err)
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(payload.Data))
	for _, d := range payload.Data {
		id := strings.TrimSpace(d.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

var statusInText = regexp.MustCompile(`\b(400|401|402|403|404|429|500|502|503|529)\b`)

// ExplainError turns a model request failure into one plain sentence with
// the next step. status is the HTTP status when the caller captured it.
func ExplainError(err error, status int) string {
	if err == nil {
		return ""
	}
	var se *StatusError
	if errors.As(err, &se) {
		status = se.Code
	}
	msg := strings.ToLower(err.Error())
	if status == 0 {
		if m := statusInText.FindString(msg); m != "" {
			fmt.Sscan(m, &status)
		}
	}
	switch status {
	case 401:
		return "密钥不对：检查 API Key 是否复制完整、是否属于这个服务商"
	case 403:
		return "密钥没有权限：在服务商后台确认已开通这个模型"
	case 402:
		return "账户余额不足：充值后再试"
	case 404:
		return "找不到接口或模型：检查接口地址（通常以 /v1 结尾）和模型名称"
	case 429:
		return "请求太频繁或额度用完：稍后再试，或检查账户余额和限额"
	case 400:
		if strings.Contains(msg, "model") {
			return "服务商不认这个模型名称：从列表里选一个，或检查拼写"
		}
		return "服务商拒绝了请求：检查模型名称和高级设置"
	case 500, 502, 503, 529:
		return "服务商暂时不可用：稍后再试"
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout"):
		return "连接超时：检查接口地址和网络，国外服务可能需要代理"
	case strings.Contains(msg, "connection refused"):
		return "连不上接口地址：检查地址和端口；用 Ollama 时先在那台电脑上启动 Ollama"
	case strings.Contains(msg, "no such host"):
		return "找不到这个地址：检查接口地址是否写对"
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "tls"):
		return "证书校验失败：检查接口地址是 http 还是 https"
	case strings.Contains(msg, "unexpected model list response") || strings.Contains(msg, "invalid character"):
		return "接口地址返回的不是模型服务的数据：检查接口地址"
	}
	return "调用失败：" + err.Error()
}
