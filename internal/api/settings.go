package api

import (
	"net/http"
	"strings"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"

	"github.com/gin-gonic/gin"
)

type notificationSettingsResponse struct {
	Telegram struct {
		Enabled  bool   `json:"enabled"`
		BotToken string `json:"bot_token"`
		ChatID   int64  `json:"chat_id"`
		AdminID  int64  `json:"admin_id"`
		BaseURL  string `json:"base_url"`
		Proxy    string `json:"proxy"`
	} `json:"telegram"`
	Feishu struct {
		Enabled   bool     `json:"enabled"`
		AppID     string   `json:"app_id"`
		AppSecret string   `json:"app_secret"`
		ChatIDs   []string `json:"chat_ids"`
	} `json:"feishu"`
	QQ struct {
		Enabled   bool   `json:"enabled"`
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
		GroupIDs  string `json:"group_ids"`
		DirectIDs string `json:"direct_ids"`
	} `json:"qq"`
	Webhook struct {
		Enabled      bool              `json:"enabled"`
		URLs         []string          `json:"urls"`
		Secret       string            `json:"secret"`
		TimeoutMs    int               `json:"timeout_ms"`
		RetryMax     int               `json:"retry_max"`
		TextTemplate string            `json:"text_template"`
		Headers      map[string]string `json:"headers,omitempty"`
	} `json:"webhook"`
	Weixin struct {
		Enabled        bool     `json:"enabled"`
		BaseURL        string   `json:"base_url"`
		AllowedUserIDs []string `json:"allowed_user_ids"`
	} `json:"weixin"`
	Bark struct {
		Enabled bool     `json:"enabled"`
		URLs    []string `json:"urls"`
		Group   string   `json:"group"`
		Icon    string   `json:"icon"`
		Level   string   `json:"level"`
	} `json:"bark"`
	Email struct {
		Enabled     bool     `json:"enabled"`
		UseSSL      bool     `json:"use_ssl"`
		SMTPHost    string   `json:"smtp_host"`
		SMTPPort    int      `json:"smtp_port"`
		Username    string   `json:"username"`
		Password    string   `json:"password"`
		FromAddress string   `json:"from_address"`
		ToAddresses []string `json:"to_addresses"`
	} `json:"email"`
	Pushplus struct {
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
		Topic   string `json:"topic"`
		Channel string `json:"channel"`
	} `json:"pushplus"`
}

type updateNotificationSettingsRequest struct {
	Telegram struct {
		Enabled  bool   `json:"enabled"`
		BotToken string `json:"bot_token"`
		ChatID   int64  `json:"chat_id"`
		AdminID  int64  `json:"admin_id"`
		BaseURL  string `json:"base_url"`
		Proxy    string `json:"proxy"` // HTTP 代理
	} `json:"telegram"`
	Feishu struct {
		Enabled   bool     `json:"enabled"`
		AppID     string   `json:"app_id"`
		AppSecret string   `json:"app_secret"`
		ChatIDs   []string `json:"chat_ids"`
	} `json:"feishu"`
	QQ struct {
		Enabled   bool   `json:"enabled"`
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
		GroupIDs  string `json:"group_ids"`
		DirectIDs string `json:"direct_ids"`
	} `json:"qq"`
	Webhook struct {
		Enabled      bool              `json:"enabled"`
		URLs         []string          `json:"urls"`
		Secret       string            `json:"secret"`
		TimeoutMs    int               `json:"timeout_ms"`
		RetryMax     int               `json:"retry_max"`
		TextTemplate string            `json:"text_template"`
		Headers      map[string]string `json:"headers,omitempty"`
	} `json:"webhook"`

	Bark struct {
		Enabled bool     `json:"enabled"`
		URLs    []string `json:"urls"`
		Group   string   `json:"group"`
		Icon    string   `json:"icon"`
		Level   string   `json:"level"`
	} `json:"bark"`
	Email struct {
		Enabled     bool     `json:"enabled"`
		UseSSL      bool     `json:"use_ssl"`
		SMTPHost    string   `json:"smtp_host"`
		SMTPPort    int      `json:"smtp_port"`
		Username    string   `json:"username"`
		Password    string   `json:"password"`
		FromAddress string   `json:"from_address"`
		ToAddresses []string `json:"to_addresses"`
	} `json:"email"`
	Pushplus struct {
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
		Topic   string `json:"topic"`
		Channel string `json:"channel"`
	} `json:"pushplus"`
}

func (s *Server) handleGetNotificationSettings(c *gin.Context) {
	var resp notificationSettingsResponse
	resp.Telegram.Enabled = s.fullCfg.Telegram.Enabled
	resp.Telegram.BotToken = s.fullCfg.Telegram.BotToken
	resp.Telegram.ChatID = s.fullCfg.Telegram.ChatID
	resp.Telegram.AdminID = s.fullCfg.Telegram.AdminID
	resp.Telegram.BaseURL = s.fullCfg.Telegram.BaseURL
	resp.Telegram.Proxy = s.fullCfg.Telegram.Proxy

	resp.Feishu.Enabled = s.fullCfg.Feishu.Enabled
	resp.Feishu.AppID = s.fullCfg.Feishu.AppID
	resp.Feishu.AppSecret = s.fullCfg.Feishu.AppSecret
	resp.Feishu.ChatIDs = s.fullCfg.Feishu.ChatIDs

	resp.QQ.Enabled = s.fullCfg.QQ.Enabled
	resp.QQ.AppID = s.fullCfg.QQ.AppID
	resp.QQ.AppSecret = s.fullCfg.QQ.AppSecret
	resp.QQ.GroupIDs = s.fullCfg.QQ.GroupIDs
	resp.QQ.DirectIDs = s.fullCfg.QQ.DirectIDs

	resp.Webhook.Enabled = s.fullCfg.Webhook.Enabled
	resp.Webhook.URLs = s.fullCfg.Webhook.URLs
	resp.Webhook.Secret = s.fullCfg.Webhook.Secret
	resp.Webhook.TimeoutMs = s.fullCfg.Webhook.TimeoutMs
	resp.Webhook.RetryMax = s.fullCfg.Webhook.RetryMax
	resp.Webhook.TextTemplate = s.fullCfg.Webhook.TextTemplate
	resp.Webhook.Headers = s.fullCfg.Webhook.Headers
	resp.Bark.Enabled = s.fullCfg.Bark.Enabled
	resp.Bark.URLs = s.fullCfg.Bark.URLs
	resp.Bark.Group = s.fullCfg.Bark.Group
	resp.Bark.Icon = s.fullCfg.Bark.Icon
	resp.Bark.Level = s.fullCfg.Bark.Level

	resp.Email.Enabled = s.fullCfg.Email.Enabled
	resp.Email.UseSSL = s.fullCfg.Email.UseSSL
	resp.Email.SMTPHost = s.fullCfg.Email.SMTPHost
	resp.Email.SMTPPort = s.fullCfg.Email.SMTPPort
	resp.Email.Username = s.fullCfg.Email.Username
	resp.Email.Password = s.fullCfg.Email.Password
	resp.Email.FromAddress = s.fullCfg.Email.FromAddress
	resp.Email.ToAddresses = append([]string(nil), s.fullCfg.Email.ToAddresses...)

	resp.Pushplus.Enabled = s.fullCfg.Pushplus.Enabled
	resp.Pushplus.Token = s.fullCfg.Pushplus.Token
	resp.Pushplus.Topic = s.fullCfg.Pushplus.Topic
	resp.Pushplus.Channel = s.fullCfg.Pushplus.Channel

	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleUpdateNotificationSettings(c *gin.Context) {
	var req updateNotificationSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数错误"})
		return
	}

	tg := config.TelegramConfig{
		Enabled:  req.Telegram.Enabled,
		BotToken: strings.TrimSpace(req.Telegram.BotToken),
		ChatID:   req.Telegram.ChatID,
		AdminID:  req.Telegram.AdminID,
		BaseURL:  strings.TrimSpace(req.Telegram.BaseURL),
		Proxy:    strings.TrimSpace(req.Telegram.Proxy),
	}

	var fsChatIDs []string
	for _, id := range req.Feishu.ChatIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			fsChatIDs = append(fsChatIDs, id)
		}
	}

	fs := config.FeishuConfig{
		Enabled:   req.Feishu.Enabled,
		AppID:     strings.TrimSpace(req.Feishu.AppID),
		AppSecret: strings.TrimSpace(req.Feishu.AppSecret),
		ChatIDs:   fsChatIDs,
	}

	qq := config.QQConfig{
		Enabled:   req.QQ.Enabled,
		AppID:     strings.TrimSpace(req.QQ.AppID),
		AppSecret: strings.TrimSpace(req.QQ.AppSecret),
		GroupIDs:  strings.TrimSpace(req.QQ.GroupIDs),
		DirectIDs: strings.TrimSpace(req.QQ.DirectIDs),
	}

	whURLs := make([]string, 0, len(req.Webhook.URLs))
	for _, u := range req.Webhook.URLs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		whURLs = append(whURLs, u)
	}

	wh := config.WebhookConfig{
		Enabled:      req.Webhook.Enabled,
		URLs:         whURLs,
		Secret:       strings.TrimSpace(req.Webhook.Secret),
		TimeoutMs:    req.Webhook.TimeoutMs,
		RetryMax:     req.Webhook.RetryMax,
		TextTemplate: req.Webhook.TextTemplate,
		Headers:      req.Webhook.Headers,
	}

	barkURLs := make([]string, 0, len(req.Bark.URLs))
	for _, u := range req.Bark.URLs {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		barkURLs = append(barkURLs, u)
	}

	barkCfg := config.BarkConfig{
		Enabled: req.Bark.Enabled,
		URLs:    barkURLs,
		Group:   strings.TrimSpace(req.Bark.Group),
		Icon:    strings.TrimSpace(req.Bark.Icon),
		Level:   strings.TrimSpace(req.Bark.Level),
	}

	emailTo := make([]string, 0, len(req.Email.ToAddresses))
	for _, a := range req.Email.ToAddresses {
		a = strings.TrimSpace(a)
		if a != "" {
			emailTo = append(emailTo, a)
		}
	}
	em := config.EmailConfig{
		Enabled:     req.Email.Enabled,
		UseSSL:      req.Email.UseSSL,
		SMTPHost:    strings.TrimSpace(req.Email.SMTPHost),
		SMTPPort:    req.Email.SMTPPort,
		Username:    strings.TrimSpace(req.Email.Username),
		Password:    strings.TrimSpace(req.Email.Password),
		FromAddress: strings.TrimSpace(req.Email.FromAddress),
		ToAddresses: emailTo,
	}

	pp := config.PushplusConfig{
		Enabled: req.Pushplus.Enabled,
		Token:   strings.TrimSpace(req.Pushplus.Token),
		Topic:   strings.TrimSpace(req.Pushplus.Topic),
		Channel: strings.TrimSpace(req.Pushplus.Channel),
	}

	if tg.Enabled {
		if tg.BotToken == "" || tg.ChatID == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Telegram 启用时必须填写 bot_token 与 chat_id"})
			return
		}
	}

	if fs.Enabled {
		if fs.AppID == "" || fs.AppSecret == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "飞书启用时必须填写 app_id 与 app_secret"})
			return
		}
	}

	if qq.Enabled {
		if qq.AppID == "" || qq.AppSecret == "" {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "QQ 启用时必须填写 app_id 与 app_secret"})
			return
		}
	}

	if wh.Enabled && len(wh.URLs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Webhook 启用时至少需要一个 URL"})
		return
	}

	if barkCfg.Enabled && len(barkCfg.URLs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Bark 启用时至少需要一个 URL"})
		return
	}

	if em.Enabled && (em.SMTPHost == "" || em.FromAddress == "" || len(em.ToAddresses) == 0) {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Email 启用时必须填写 SMTP Host、发件人及收件人"})
		return
	}

	if pp.Enabled && pp.Token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "Pushplus 启用时必须填写 Token"})
		return
	}

	if err := config.UpdateNotificationInFile(s.configPath, tg, fs, qq, wh, barkCfg, em, pp); err != nil {
		logger.Error("写入通知配置失败", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "写入配置文件失败: " + err.Error()})
		return
	}

	s.fullCfg.Telegram = tg
	s.fullCfg.Feishu = fs
	s.fullCfg.QQ = qq
	s.fullCfg.Webhook = wh
	s.fullCfg.Bark = barkCfg
	s.fullCfg.Email = em
	s.fullCfg.Pushplus = pp

	if s.notifyMgr != nil {
		if err := s.notifyMgr.UpdateConfig(s.fullCfg); err != nil {
			logger.Error("应用通知配置失败", "err", err)
			c.JSON(http.StatusOK, gin.H{
				"status":  "ok",
				"applied": false,
				"warning": "通知配置已写入，但运行时初始化失败: " + err.Error(),
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "applied": true})
}
