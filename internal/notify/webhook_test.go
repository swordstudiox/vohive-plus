package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

// TestWebhookSignature 验证 HMAC-SHA256 签名的正确性
func TestWebhookSignature(t *testing.T) {
	secret := "test-secret-key"
	var receivedSig string
	var receivedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Vohive-Signature")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := NewWebhookChannel(config.WebhookConfig{
		Enabled: true,
		URLs:    []string{srv.URL},
		Secret:  secret,
	})
	if err != nil {
		t.Fatalf("创建 WebhookChannel 失败: %v", err)
	}

	if err := ch.Send("测试消息"); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}

	// 手动计算期望签名
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if receivedSig != expected {
		t.Errorf("签名不匹配\n期望: %s\n实际: %s", expected, receivedSig)
	}
}

// TestWebhookNoSignatureWhenSecretEmpty 验证 secret 为空时不携带签名 header
func TestWebhookNoSignatureWhenSecretEmpty(t *testing.T) {
	var hasSigHeader bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasSigHeader = r.Header.Get("X-Vohive-Signature") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := NewWebhookChannel(config.WebhookConfig{
		Enabled: true,
		URLs:    []string{srv.URL},
		Secret:  "", // 空 secret
	})
	if err != nil {
		t.Fatalf("创建 WebhookChannel 失败: %v", err)
	}

	if err := ch.Send("测试消息"); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}

	if hasSigHeader {
		t.Error("secret 为空时不应携带 X-Vohive-Signature header")
	}
}

// TestWebhookPayloadFormat 验证 JSON payload 格式正确
func TestWebhookPayloadFormat(t *testing.T) {
	var receivedBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)

		// 验证 Content-Type
		ct := r.Header.Get("Content-Type")
		if ct != "application/json" {
			t.Errorf("Content-Type 不正确: %s", ct)
		}

		// 验证 User-Agent
		ua := r.Header.Get("User-Agent")
		if ua != "Vohive-Webhook/1.0" {
			t.Errorf("User-Agent 不正确: %s", ua)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, _ := NewWebhookChannel(config.WebhookConfig{
		Enabled: true,
		URLs:    []string{srv.URL},
	})

	testText := "📩 收到新短信\n设备: ec20_1"
	if err := ch.Send(testText); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}

	var payload webhookPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("解析 payload 失败: %v", err)
	}

	if payload.Event != "notification" {
		t.Errorf("event 字段错误: %s", payload.Event)
	}
	if payload.Text != testText {
		t.Errorf("text 字段错误: %s", payload.Text)
	}
	if payload.Timestamp == "" {
		t.Error("timestamp 字段为空")
	}
	if payload.Meta.Event != payload.Event {
		t.Errorf("meta.event 错误: %s", payload.Meta.Event)
	}
	if payload.Meta.DeviceLabel == "" {
		t.Error("meta.device_label 不能为空")
	}
}

// TestWebhookRetryOn5xx 验证 5xx 错误触发重试
func TestWebhookRetryOn5xx(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		n := callCount.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError) // 前两次返回 500
		} else {
			w.WriteHeader(http.StatusOK) // 第三次成功
		}
	}))
	defer srv.Close()

	ch, _ := NewWebhookChannel(config.WebhookConfig{
		Enabled:   true,
		URLs:      []string{srv.URL},
		RetryMax:  3,
		TimeoutMs: 5000,
	})

	if err := ch.Send("重试测试"); err != nil {
		t.Fatalf("Send 失败（应在第三次成功）: %v", err)
	}

	if count := callCount.Load(); count != 3 {
		t.Errorf("期望请求 3 次，实际 %d 次", count)
	}
}

// TestWebhookNoRetryOn4xx 验证 4xx 错误不重试
func TestWebhookNoRetryOn4xx(t *testing.T) {
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		callCount.Add(1)
		w.WriteHeader(http.StatusBadRequest) // 返回 400
	}))
	defer srv.Close()

	ch, _ := NewWebhookChannel(config.WebhookConfig{
		Enabled:   true,
		URLs:      []string{srv.URL},
		RetryMax:  3,
		TimeoutMs: 5000,
	})

	err := ch.Send("4xx 测试")
	if err == nil {
		t.Fatal("期望返回错误")
	}

	if count := callCount.Load(); count != 1 {
		t.Errorf("4xx 不应重试，期望请求 1 次，实际 %d 次", count)
	}
}

// TestWebhookMultiURLParallel 验证多 URL 并行推送
func TestWebhookMultiURLParallel(t *testing.T) {
	var count1, count2, count3 atomic.Int32

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		count1.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		count2.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv2.Close()

	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		count3.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv3.Close()

	ch, _ := NewWebhookChannel(config.WebhookConfig{
		Enabled: true,
		URLs:    []string{srv1.URL, srv2.URL, srv3.URL},
	})

	if err := ch.Send("并行测试"); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}

	if count1.Load() != 1 {
		t.Errorf("URL1 期望收到 1 次请求，实际 %d", count1.Load())
	}
	if count2.Load() != 1 {
		t.Errorf("URL2 期望收到 1 次请求，实际 %d", count2.Load())
	}
	if count3.Load() != 1 {
		t.Errorf("URL3 期望收到 1 次请求，实际 %d", count3.Load())
	}
}

// TestWebhookCustomHeaders 验证用户自定义头被正确注入
func TestWebhookCustomHeaders(t *testing.T) {
	var gotAuth, gotApiKey, gotUA string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotApiKey = r.Header.Get("X-Api-Key")
		gotUA = r.Header.Get("User-Agent")
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := NewWebhookChannel(config.WebhookConfig{
		Enabled: true,
		URLs:    []string{srv.URL},
		Headers: map[string]string{
			"Authorization": "Bearer token123",
			"X-Api-Key":     "secret-key",
			"User-Agent":    "Custom-Agent/9.9", // 自定义 UA 允许覆盖默认
		},
	})
	if err != nil {
		t.Fatalf("创建 WebhookChannel 失败: %v", err)
	}

	if err := ch.Send("自定义头测试"); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}

	if gotAuth != "Bearer token123" {
		t.Errorf("Authorization 头错误: %q", gotAuth)
	}
	if gotApiKey != "secret-key" {
		t.Errorf("X-Api-Key 头错误: %q", gotApiKey)
	}
	if gotUA != "Custom-Agent/9.9" {
		t.Errorf("User-Agent 应可被自定义覆盖，实际: %q", gotUA)
	}
}

// TestWebhookCustomHeadersCannotOverrideProtected 验证受保护的系统头不可被覆盖
func TestWebhookCustomHeadersCannotOverrideProtected(t *testing.T) {
	secret := "test-secret-key"
	var gotCT, gotSig string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotSig = r.Header.Get("X-Vohive-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := NewWebhookChannel(config.WebhookConfig{
		Enabled: true,
		URLs:    []string{srv.URL},
		Secret:  secret,
		Headers: map[string]string{
			"Content-Type":       "text/plain",    // 尝试覆盖
			"content-type":       "text/xml",      // 大小写变体也应被拦截
			"X-Vohive-Signature": "sha256=forged", // 尝试伪造签名
			"":                   "ignored",       // 空 key 应被丢弃
		},
	})
	if err != nil {
		t.Fatalf("创建 WebhookChannel 失败: %v", err)
	}

	if err := ch.Send("受保护头测试"); err != nil {
		t.Fatalf("Send 失败: %v", err)
	}

	if gotCT != "application/json" {
		t.Errorf("Content-Type 不应被覆盖，实际: %q", gotCT)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSig != expected {
		t.Errorf("签名应由系统计算而非被伪造\n期望: %s\n实际: %s", expected, gotSig)
	}
}

func TestWebhookTextTemplateRendersDeviceLabel(t *testing.T) {
	var payload webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := NewWebhookChannel(config.WebhookConfig{
		Enabled:      true,
		URLs:         []string{srv.URL},
		TextTemplate: "[{{device_label}}] {{text}}",
	})
	if err != nil {
		t.Fatalf("创建 WebhookChannel 失败: %v", err)
	}

	err = ch.SendWithContext(NotificationContext{
		Event:      "sms_received",
		Text:       "收到新短信",
		DeviceID:   "wwan0",
		DeviceName: "客厅主卡",
		Timestamp:  time.Now(),
	})
	if err != nil {
		t.Fatalf("SendWithContext 失败: %v", err)
	}

	if payload.Text != "[客厅主卡 (wwan0)] 收到新短信" {
		t.Fatalf("text=%q", payload.Text)
	}
	if payload.Meta.DeviceLabel != "客厅主卡 (wwan0)" {
		t.Fatalf("meta.device_label=%q", payload.Meta.DeviceLabel)
	}
}

func TestWebhookTemplateEmptyFallsBackToRawText(t *testing.T) {
	var payload webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := NewWebhookChannel(config.WebhookConfig{
		Enabled:      true,
		URLs:         []string{srv.URL},
		TextTemplate: "",
	})
	if err != nil {
		t.Fatalf("创建 WebhookChannel 失败: %v", err)
	}

	if err := ch.SendWithContext(NotificationContext{
		Event:     "raw",
		Text:      "原始消息",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SendWithContext 失败: %v", err)
	}

	if payload.Text != "原始消息" {
		t.Fatalf("text=%q", payload.Text)
	}
}

func TestWebhookTemplateKeepsUnknownPlaceholder(t *testing.T) {
	var payload webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := NewWebhookChannel(config.WebhookConfig{
		Enabled:      true,
		URLs:         []string{srv.URL},
		TextTemplate: "[{{unknown_key}}] {{text}}",
	})
	if err != nil {
		t.Fatalf("创建 WebhookChannel 失败: %v", err)
	}

	if err := ch.SendWithContext(NotificationContext{
		Event:     "raw",
		Text:      "hello",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("SendWithContext 失败: %v", err)
	}

	if payload.Text != "[{{unknown_key}}] hello" {
		t.Fatalf("text=%q", payload.Text)
	}
}
