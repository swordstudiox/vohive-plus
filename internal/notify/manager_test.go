package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type captureChannel struct {
	mu    sync.Mutex
	msgs  []string
	calls []NotificationContext
}

func (c *captureChannel) Name() string { return "capture" }

func (c *captureChannel) Send(text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, text)
	return nil
}

func (c *captureChannel) SendWithContext(ctx NotificationContext) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, ctx)
	c.msgs = append(c.msgs, ctx.Text)
	return nil
}

func (c *captureChannel) RegisterCommand(_ string, _ CommandHandler) {}
func (c *captureChannel) Start() error                               { return nil }
func (c *captureChannel) Close() error                               { return nil }

func (c *captureChannel) Last() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.msgs) == 0 {
		return ""
	}
	return c.msgs[len(c.msgs)-1]
}

func (c *captureChannel) LastContext() NotificationContext {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return NotificationContext{}
	}
	return c.calls[len(c.calls)-1]
}

func readLogFields(t *testing.T, entry logger.LogEntry) map[string]any {
	t.Helper()
	if entry.Fields == "" {
		return map[string]any{}
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(entry.Fields), &fields); err != nil {
		t.Fatalf("failed to parse log fields: %v", err)
	}
	return fields
}

func waitLogEntry(t *testing.T, ch <-chan logger.LogEntry, match func(entry logger.LogEntry) bool) logger.LogEntry {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case entry := <-ch:
			if match(entry) {
				return entry
			}
		case <-deadline:
			t.Fatal("matched log entry not found")
		}
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestManagerNotifyEventsToWebhookWithTemplate(t *testing.T) {
	var mu sync.Mutex
	var payloads []webhookPayload

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload webhookPayload
		_ = json.Unmarshal(body, &payload)
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wh, err := NewWebhookChannel(webhookConfigForTest(srv.URL, "[{{device_label}}] {{text}}"))
	if err != nil {
		t.Fatalf("NewWebhookChannel() error = %v", err)
	}

	m := &Manager{channels: []Channel{wh}}

	ts := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	m.NotifySMS("wwan0", "+8613800000000", "hello", ts)
	m.NotifyIPRotated("wwan0", "1.1.1.1", "2.2.2.2", 2*time.Second)
	m.NotifyRaw("raw message")

	waitUntil(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(payloads) == 3
	})
	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 3 {
		t.Fatalf("payload count=%d, want=3", len(payloads))
	}
	byEvent := make(map[string]webhookPayload, len(payloads))
	for _, payload := range payloads {
		byEvent[payload.Event] = payload
	}
	if got := byEvent["sms_received"].Text; got != "[wwan0] 收到新短信 / 蜂窝\n设备  wwan0\n号码  +8613800000000\n时间  2026-04-13 12:00:00\n内容  hello" {
		t.Fatalf("sms text=%q", got)
	}
	if got := byEvent["ip_rotated"].Meta.DeviceID; got != "wwan0" {
		t.Fatalf("ip_rotated meta.device_id=%q", got)
	}
	if _, ok := byEvent["raw"]; !ok {
		t.Fatal("raw event missing")
	}
}

func TestManagerNotifyRawKeepsPlainChannelText(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}

	m.NotifyRaw("plain channel text")
	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	if got := capture.Last(); got != "plain channel text" {
		t.Fatalf("plain channel text=%q", got)
	}
}

func TestManagerNotifyIPRotatedUsesPlainTemplate(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}

	m.NotifyIPRotated("wwan0", "1.1.1.1", "2.2.2.2", 2*time.Second)
	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	want := "公网切换 / 完成\n设备    wwan0\n旧 IP   1.1.1.1\n新 IP   2.2.2.2\n耗时    2s"
	if got := capture.Last(); got != want {
		t.Fatalf("ip rotated text=%q, want %q", got, want)
	}
}

func TestManagerNotifyIncomingCallUsesPlainTemplate(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}

	m.NotifyIncomingCall("wwan0", "10086", "10010")
	time.Sleep(20 * time.Millisecond)
	want := "来电通知\n设备    wwan0\n主叫    10086\n被叫    10010"
	if got := capture.Last(); got != want {
		t.Fatalf("incoming call text=%q, want %q", got, want)
	}
}

func TestManagerNotifySMSLogsBroadcastSummary(t *testing.T) {
	logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(t.TempDir(), "app.log")})
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}
	ch := logger.GlobalBroadcaster.Subscribe()
	defer logger.GlobalBroadcaster.Unsubscribe(ch)

	ts := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	m.NotifySMS("wwan0", "+8613800000000", "hello", ts)

	entry := waitLogEntry(t, ch, func(entry logger.LogEntry) bool {
		return entry.Message == "开始发送短信通知"
	})
	fields := readLogFields(t, entry)
	if fields["event"] != "sms_received" {
		t.Fatalf("event=%v want sms_received", fields["event"])
	}
	if fields["channel_count"] != float64(1) {
		t.Fatalf("channel_count=%v want 1", fields["channel_count"])
	}
}

func TestManagerNotifySMSWithSourceUsesProvidedSourceLabel(t *testing.T) {
	capture := &captureChannel{}
	m := &Manager{channels: []Channel{capture}}
	notifier, ok := any(m).(interface {
		NotifySMSWithSource(deviceID, sender, content, source string, timestamp time.Time)
	})
	if !ok {
		t.Fatal("NotifySMSWithSource missing")
	}

	ts := time.Date(2026, 4, 13, 12, 0, 0, 0, time.UTC)
	notifier.NotifySMSWithSource("wwan0", "+8613800000000", "hello", "VoWiFi", ts)

	waitUntil(t, time.Second, func() bool { return capture.Last() != "" })
	want := "收到新短信 / VoWiFi\n设备  wwan0\n号码  +8613800000000\n时间  2026-04-13 12:00:00\n内容  hello"
	if got := capture.Last(); got != want {
		t.Fatalf("text=%q, want %q", got, want)
	}
	if got := capture.LastContext().Event; got != "sms_received" {
		t.Fatalf("event=%q, want sms_received", got)
	}
}

func webhookConfigForTest(url, template string) config.WebhookConfig {
	return config.WebhookConfig{
		Enabled:      true,
		URLs:         []string{url},
		TimeoutMs:    5000,
		RetryMax:     0,
		TextTemplate: template,
	}
}
