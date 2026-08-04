package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/notify"
)

type testEmailRequest struct {
	Enabled     bool     `json:"enabled"`
	UseSSL      bool     `json:"use_ssl"`
	SMTPHost    string   `json:"smtp_host"`
	SMTPPort    int      `json:"smtp_port"`
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	FromAddress string   `json:"from_address"`
	ToAddresses []string `json:"to_addresses"`
}

func (s *Server) handleTestEmailNotification(c *gin.Context) {
	var req testEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "参数错误"})
		return
	}

	if !req.Enabled {
		c.JSON(http.StatusBadRequest, gin.H{"message": "请先启用 Email 后再测试"})
		return
	}

	toAddrs := make([]string, 0, len(req.ToAddresses))
	for _, a := range req.ToAddresses {
		trimmed := strings.TrimSpace(a)
		if trimmed == "" {
			continue
		}
		toAddrs = append(toAddrs, trimmed)
	}

	if len(toAddrs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"message": "至少需要一个有效的收件人地址"})
		return
	}

	ch, err := notify.NewEmailChannel(config.EmailConfig{
		Enabled:     true,
		UseSSL:      req.UseSSL,
		SMTPHost:    strings.TrimSpace(req.SMTPHost),
		SMTPPort:    req.SMTPPort,
		Username:    strings.TrimSpace(req.Username),
		Password:    strings.TrimSpace(req.Password),
		FromAddress: strings.TrimSpace(req.FromAddress),
		ToAddresses: toAddrs,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "初始化 Email 测试发送器失败: " + err.Error()})
		return
	}

	now := time.Now()
	ctx := notify.NotificationContext{
		Event:      "测试",
		Text:       "这是一条来自 Vohive 的邮件测试通知，收到说明您的邮件配置正确！",
		DeviceID:   "test_device_001",
		DeviceName: "测试设备",
		Timestamp:  now,
	}

	sendErr := ch.SendWithContext(ctx)
	if sendErr != nil {
		c.JSON(http.StatusOK, gin.H{
			"ok":      false,
			"message": "测试邮件发送失败: " + sendErr.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"message": "测试邮件已发送",
	})
}
