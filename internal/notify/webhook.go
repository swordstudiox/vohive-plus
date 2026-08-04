package notify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

// webhookPayload 定义 Webhook 推送的 JSON 结构
type webhookPayload struct {
	Event     string             `json:"event"`     // 事件类型
	Timestamp string             `json:"timestamp"` // ISO 8601 时间戳
	Text      string             `json:"text"`      // 通知文本内容
	Meta      webhookPayloadMeta `json:"meta"`      // 结构化字段，便于下游解析
}

type webhookPayloadMeta struct {
	DeviceID    string `json:"device_id,omitempty"`
	DeviceName  string `json:"device_name,omitempty"`
	DeviceLabel string `json:"device_label"`
	Event       string `json:"event"`
	Timestamp   string `json:"timestamp"`
}

// WebhookChannel 实现 Channel 接口的 Webhook 通知渠道
// 通过 HTTP POST 将通知推送到用户配置的 URL 列表
// 该渠道为「只出不进」—— 仅推送通知，不支持接收命令
type WebhookChannel struct {
	urls         []string                  // 目标 URL 列表
	secret       string                    // HMAC-SHA256 签名密钥，为空时不签名
	textTemplate string                    // 文本模板（为空则透传原文）
	headers      map[string]string         // 用户自定义请求头（不允许覆盖系统头）
	client       *http.Client              // 带超时的 HTTP 客户端
	retryMax     int                       // 最大重试次数
	handlers     map[string]CommandHandler // 空实现占位
}

// protectedWebhookHeaders 是系统强制设置、不允许被自定义头覆盖的请求头（小写）
var protectedWebhookHeaders = map[string]struct{}{
	"content-type":       {},
	"x-vohive-signature": {},
}

type SendWithContextResult struct {
	FailedURLs []string
}

var webhookPlaceholderPattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// NewWebhookChannel 根据配置创建 Webhook 渠道
func NewWebhookChannel(cfg config.WebhookConfig) (*WebhookChannel, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if len(cfg.URLs) == 0 {
		logger.Warn("Webhook 渠道已启用但未配置 urls，消息不会推送")
	}

	timeoutMs := cfg.TimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	retryMax := cfg.RetryMax
	if retryMax < 0 {
		retryMax = 0
	}

	ch := &WebhookChannel{
		urls:         cfg.URLs,
		secret:       cfg.Secret,
		textTemplate: cfg.TextTemplate,
		headers:      sanitizeWebhookHeaders(cfg.Headers),
		retryMax:     retryMax,
		handlers:     make(map[string]CommandHandler),
		client: &http.Client{
			Timeout: time.Duration(timeoutMs) * time.Millisecond,
		},
	}

	logger.Info("Webhook 渠道已创建", "urls", len(cfg.URLs), "timeout_ms", timeoutMs, "retry_max", retryMax)
	return ch, nil
}

func (w *WebhookChannel) Name() string { return "webhook" }

// Send 构建 JSON payload 并向所有 URL 并行推送
func (w *WebhookChannel) Send(text string) error {
	return w.SendWithContext(NotificationContext{
		Event:     "notification",
		Text:      text,
		Timestamp: time.Now(),
	})
}

func (w *WebhookChannel) SendWithContext(ctx NotificationContext) error {
	_, err := w.SendWithContextDetailed(ctx)
	return err
}

func (w *WebhookChannel) SendWithContextDetailed(ctx NotificationContext) (SendWithContextResult, error) {
	result := SendWithContextResult{}
	if w == nil || w.client == nil || len(w.urls) == 0 {
		return result, nil
	}

	text := strings.TrimSpace(ctx.Text)
	if text == "" {
		return result, nil
	}
	if ctx.Timestamp.IsZero() {
		ctx.Timestamp = time.Now()
	}
	event := strings.TrimSpace(ctx.Event)
	if event == "" {
		event = "notification"
	}

	textForPush := text
	if strings.TrimSpace(w.textTemplate) != "" {
		rendered, err := w.renderText(ctx)
		if err != nil {
			logger.Warn("渲染 Webhook 文本模板失败，回退原文", "err", err, "event", event)
		} else if strings.TrimSpace(rendered) != "" {
			textForPush = rendered
		}
	}

	ts := ctx.Timestamp.Format(time.RFC3339)
	payload := webhookPayload{
		Event:     event,
		Timestamp: ts,
		Text:      textForPush,
		Meta: webhookPayloadMeta{
			DeviceID:    strings.TrimSpace(ctx.DeviceID),
			DeviceName:  strings.TrimSpace(ctx.DeviceName),
			DeviceLabel: ctx.DeviceLabel(),
			Event:       event,
			Timestamp:   ts,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return result, fmt.Errorf("序列化 webhook payload 失败: %w", err)
	}

	// 预计算 HMAC 签名（所有 URL 共用同一份 body，签名只算一次）
	signature := w.computeSignature(body)

	// 多 URL 并行推送
	var wg sync.WaitGroup
	var mu sync.Mutex
	var lastErr error
	failedURLs := make([]string, 0)

	for _, u := range w.urls {
		wg.Add(1)
		go func(targetURL string) {
			defer wg.Done()
			if err := w.postWithRetry(targetURL, body, signature); err != nil {
				mu.Lock()
				lastErr = fmt.Errorf("%s: %w", targetURL, err)
				failedURLs = append(failedURLs, targetURL)
				mu.Unlock()
				logger.Warn("Webhook 推送失败", "url", targetURL, "err", err)
			}
		}(u)
	}

	wg.Wait()
	result.FailedURLs = failedURLs
	return result, lastErr
}

func (w *WebhookChannel) renderText(ctx NotificationContext) (rendered string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("模板渲染异常: %v", r)
		}
	}()
	values := map[string]string{
		"text":         strings.TrimSpace(ctx.Text),
		"event":        strings.TrimSpace(ctx.Event),
		"timestamp":    ctx.Timestamp.Format(time.RFC3339),
		"device_id":    strings.TrimSpace(ctx.DeviceID),
		"device_name":  strings.TrimSpace(ctx.DeviceName),
		"device_label": ctx.DeviceLabel(),
	}
	rendered = webhookPlaceholderPattern.ReplaceAllStringFunc(w.textTemplate, func(token string) string {
		matches := webhookPlaceholderPattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		key := matches[1]
		if v, ok := values[key]; ok {
			return v
		}
		return token
	})
	return rendered, nil
}

// RegisterCommand 空实现 — Webhook 渠道不支持接收命令
func (w *WebhookChannel) RegisterCommand(_ string, _ CommandHandler) {}

// Start 空实现 — Webhook 渠道无需命令监听
func (w *WebhookChannel) Start() error { return nil }

// Close 关闭 HTTP 客户端的空闲连接
func (w *WebhookChannel) Close() error {
	if w != nil && w.client != nil {
		w.client.CloseIdleConnections()
	}
	return nil
}

// postWithRetry 向目标 URL 发送 POST 请求，对 5xx 和网络错误执行指数退避重试
func (w *WebhookChannel) postWithRetry(targetURL string, body []byte, signature string) error {
	var lastErr error

	for attempt := 0; attempt <= w.retryMax; attempt++ {
		if attempt > 0 {
			// 指数退避：1s, 2s, 4s, ...
			backoff := time.Duration(1<<(attempt-1)) * time.Second
			time.Sleep(backoff)
		}

		statusCode, err := w.doPost(targetURL, body, signature)
		if err != nil {
			lastErr = err
			logger.Debug("Webhook POST 失败，准备重试",
				"url", targetURL, "attempt", attempt+1, "err", err)
			continue
		}

		// 2xx 成功
		if statusCode >= 200 && statusCode < 300 {
			return nil
		}

		// 4xx 客户端错误，不重试
		if statusCode >= 400 && statusCode < 500 {
			return fmt.Errorf("webhook 返回 %d，不重试", statusCode)
		}

		// 5xx 服务端错误，继续重试
		lastErr = fmt.Errorf("webhook 返回 %d", statusCode)
		logger.Debug("Webhook 返回 5xx，准备重试",
			"url", targetURL, "attempt", attempt+1, "status", statusCode)
	}

	return fmt.Errorf("webhook 推送失败（已重试 %d 次）: %w", w.retryMax, lastErr)
}

// doPost 执行单次 HTTP POST 请求
func (w *WebhookChannel) doPost(targetURL string, body []byte, signature string) (int, error) {
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}

	// 先注入用户自定义头，随后由系统头覆盖（受保护的系统头始终生效）
	for k, v := range w.headers {
		req.Header.Set(k, v)
	}

	req.Header.Set("Content-Type", "application/json")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Vohive-Webhook/1.0")
	}
	if signature != "" {
		req.Header.Set("X-Vohive-Signature", signature)
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	// 读取并丢弃 response body，确保连接可复用
	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode, nil
}

// sanitizeWebhookHeaders 清洗用户自定义头：去除空白 key、丢弃受保护的系统头
// 返回 nil 表示无有效自定义头
func sanitizeWebhookHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		if _, protected := protectedWebhookHeaders[strings.ToLower(key)]; protected {
			logger.Warn("Webhook 自定义头与受保护的系统头冲突，已忽略", "header", key)
			continue
		}
		out[key] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// computeSignature 使用 HMAC-SHA256 计算请求体签名
// 若 secret 为空则返回空字符串（不签名）
func (w *WebhookChannel) computeSignature(body []byte) string {
	if w.secret == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(w.secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
