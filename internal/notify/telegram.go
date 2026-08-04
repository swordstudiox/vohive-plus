package notify

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramChannel 实现 Channel 接口的 Telegram 通知渠道
type TelegramChannel struct {
	api      *tgbotapi.BotAPI
	chatID   int64
	handlers map[string]CommandHandler
}

// NewTelegramChannel 根据配置创建 Telegram 渠道
func NewTelegramChannel(cfg config.TelegramConfig) (*TelegramChannel, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	endpoint := tgbotapi.APIEndpoint
	if cfg.BaseURL != "" {
		endpoint = cfg.BaseURL
		if !strings.Contains(endpoint, "bot%s/%s") {
			endpoint = strings.TrimSuffix(endpoint, "/") + "/bot%s/%s"
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Proxy != "" {
		proxyURL, err := url.Parse(cfg.Proxy)
		if err != nil {
			logger.Error("解析 Telegram 代理地址失败", "proxy", cfg.Proxy, "err", err)
		} else {
			transport.Proxy = http.ProxyURL(proxyURL)
			logger.Info("Telegram Bot 使用代理", "proxy", cfg.Proxy)
		}
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   120 * time.Second,
	}

	bot, err := tgbotapi.NewBotAPIWithClient(cfg.BotToken, endpoint, httpClient)
	if err != nil {
		msg := err.Error()
		if cfg.BotToken != "" {
			msg = strings.ReplaceAll(msg, cfg.BotToken, "<redacted>")
		}
		return nil, fmt.Errorf("创建 telegram bot 失败: %s", msg)
	}

	logger.Info("已授权账户 (TG)", "username", bot.Self.UserName)

	return &TelegramChannel{
		api:      bot,
		chatID:   cfg.ChatID,
		handlers: make(map[string]CommandHandler),
	}, nil
}

func (t *TelegramChannel) Name() string { return "telegram" }

func buildTelegramTextMessage(chatID int64, text string) tgbotapi.MessageConfig {
	// 过滤非法的 UTF-8 字符，防止 Telegram API 报错
	cleanText := strings.Map(func(r rune) rune {
		if r == utf8.RuneError {
			return -1 // 丢弃非法字符
		}
		return r
	}, text)

	escaped := html.EscapeString(cleanText)
	msg := tgbotapi.NewMessage(chatID, escaped)
	// 使用 HTML 模式，但先转义短信原文，避免 "<#>" 等内容被当作标签解析。
	msg.ParseMode = "HTML"
	return msg
}

func (t *TelegramChannel) Send(text string) error {
	if t == nil || t.api == nil {
		return nil
	}

	msg := buildTelegramTextMessage(t.chatID, text)
	_, err := t.api.Send(msg)
	if err != nil {
		logger.Error("发送 telegram 消息失败", "err", err)
		return err
	}
	return nil
}

func (t *TelegramChannel) RegisterCommand(cmd string, handler CommandHandler) {
	if t == nil {
		return
	}
	t.handlers[cmd] = handler
	logger.Info("注册 Telegram 命令", "command", "/"+cmd)
}

// tgCommandContext 实现了 CommandContext 接口
type tgCommandContext struct {
	channel *TelegramChannel
}

func (c *tgCommandContext) Reply(text string) {
	if c == nil || c.channel == nil {
		return
	}
	// 避免 Telegram API 短时阻塞拖住命令处理主循环
	go func() {
		_ = c.channel.Send(text)
	}()
}

// Start 启动 Telegram long-polling 命令监听（阻塞式）
func (t *TelegramChannel) Start() error {
	if t == nil || t.api == nil {
		return nil
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := t.api.GetUpdatesChan(u)
	logger.Info("Telegram Bot 命令监听已启动")

	for update := range updates {
		if update.Message == nil || !update.Message.IsCommand() {
			continue
		}

		// 仅处理来自授权用户的命令
		if update.Message.Chat.ID != t.chatID {
			continue
		}

		command := update.Message.Command()
		args := strings.Fields(update.Message.CommandArguments())

		logger.Info("收到 Telegram 命令", "command", command, "args", args)

		handler, ok := t.handlers[command]
		if !ok {
			ctx := &tgCommandContext{channel: t}
			ctx.Reply(unknownCommandReply(command))
			continue
		}

		ctx := &tgCommandContext{channel: t}
		response := handler(ctx, args)
		if response != "" {
			ctx.Reply(response)
		}
	}
	return nil
}

func (t *TelegramChannel) Close() error {
	if t == nil || t.api == nil {
		return nil
	}
	t.api.StopReceivingUpdates()
	return nil
}
