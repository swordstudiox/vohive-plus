package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/notify"
)

type testBarkRequest struct {
	Enabled bool     `json:"enabled"`
	URLs    []string `json:"urls"`
	Group   string   `json:"group"`
	Icon    string   `json:"icon"`
	Level   string   `json:"level"`
}

type testBarkResponse struct {
	OK         bool     `json:"ok"`
	Message    string   `json:"message"`
	FailedURLs []string `json:"failed_urls,omitempty"`
}

func (s *Server) handleTestBarkNotification(c *gin.Context) {
	var req testBarkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}

	if !req.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请先启用 Bark 后再测试"})
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
		c.JSON(http.StatusBadRequest, gin.H{"message": "至少需要一个有效的 Bark URL"})
		return
	}

	ch, err := notify.NewBarkChannel(config.BarkConfig{
		Enabled: true,
		URLs:    urls,
		Group:   strings.TrimSpace(req.Group),
		Icon:    strings.TrimSpace(req.Icon),
		Level:   strings.TrimSpace(req.Level),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "初始化 Bark 测试发送器失败: " + err.Error()})
		return
	}
	if ch == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "Bark 测试发送器未初始化"})
		return
	}
	defer ch.Close()

	now := time.Now()
	ctx := notify.NotificationContext{
		Event:      "bark_test",
		Text:       "这是一条 Bark 测试通知",
		DeviceID:   "test_device_001",
		DeviceName: "测试设备",
		Timestamp:  now,
	}

	result, sendErr := ch.SendWithContextDetailed(ctx)
	if sendErr != nil {
		c.JSON(http.StatusOK, testBarkResponse{
			OK:         false,
			Message:    "测试通知发送失败: " + sendErr.Error(),
			FailedURLs: result.FailedURLs,
		})
		return
	}

	c.JSON(http.StatusOK, testBarkResponse{
		OK:      true,
		Message: "测试通知已发送",
	})
}
