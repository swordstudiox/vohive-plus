package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/notify"
)

type testWebhookRequest struct {
	Enabled      bool              `json:"enabled"`
	URLs         []string          `json:"urls"`
	Secret       string            `json:"secret"`
	TimeoutMs    int               `json:"timeout_ms"`
	RetryMax     int               `json:"retry_max"`
	TextTemplate string            `json:"text_template"`
	Headers      map[string]string `json:"headers,omitempty"`
}

type testWebhookResponse struct {
	OK         bool     `json:"ok"`
	Message    string   `json:"message"`
	FailedURLs []string `json:"failed_urls,omitempty"`
}

func (s *Server) handleTestWebhookNotification(c *gin.Context) {
	var req testWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}

	if !req.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请先启用 Webhook 后再测试"})
		return
	}

	urls := make([]string, 0, len(req.URLs))
	for _, u := range req.URLs {
		trimmed := strings.TrimSpace(u)
		if trimmed == "" {
			continue
		}
		urls = append(urls, trimmed)
	}
	if len(urls) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "至少需要一个有效的 Webhook URL"})
		return
	}

	if req.TimeoutMs < 1000 || req.TimeoutMs > 60000 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "timeout_ms 必须在 1000-60000 之间"})
		return
	}
	if req.RetryMax < 0 || req.RetryMax > 10 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "retry_max 必须在 0-10 之间"})
		return
	}

	ch, err := notify.NewWebhookChannel(config.WebhookConfig{
		Enabled:      true,
		URLs:         urls,
		Secret:       strings.TrimSpace(req.Secret),
		TimeoutMs:    req.TimeoutMs,
		RetryMax:     req.RetryMax,
		TextTemplate: req.TextTemplate,
		Headers:      req.Headers,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "初始化 Webhook 测试发送器失败: " + err.Error()})
		return
	}
	if ch == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Webhook 测试发送器未初始化"})
		return
	}
	defer ch.Close()

	now := time.Now()
	ctx := notify.NotificationContext{
		Event:      "webhook_test",
		Text:       "这是一条 Webhook 测试通知",
		DeviceID:   "test_device_001",
		DeviceName: "测试设备",
		Timestamp:  now,
	}

	result, sendErr := ch.SendWithContextDetailed(ctx)
	if sendErr != nil {
		c.JSON(http.StatusOK, testWebhookResponse{
			OK:         false,
			Message:    "测试通知发送失败: " + sendErr.Error(),
			FailedURLs: result.FailedURLs,
		})
		return
	}

	c.JSON(http.StatusOK, testWebhookResponse{
		OK:      true,
		Message: "测试通知已发送",
	})
}
