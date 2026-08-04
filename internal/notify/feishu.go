//go:build !(linux && arm)

package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// feishuLogger 将飞书 SDK 日志转发到 vohive 的 logger
type feishuLogger struct{}

func (l *feishuLogger) Debug(ctx context.Context, args ...interface{}) {
	logger.Debug("[飞书] " + fmt.Sprint(args...))
}
func (l *feishuLogger) Info(ctx context.Context, args ...interface{}) {
	logger.Info("[飞书] " + fmt.Sprint(args...))
}
func (l *feishuLogger) Warn(ctx context.Context, args ...interface{}) {
	logger.Warn("[飞书] " + fmt.Sprint(args...))
}
func (l *feishuLogger) Error(ctx context.Context, args ...interface{}) {
	logger.Error("[飞书] " + fmt.Sprint(args...))
}

// FeishuChannel 实现 Channel 接口的飞书通知渠道
// 使用飞书开放平台 Bot + WebSocket 长连接
type FeishuChannel struct {
	client   *lark.Client
	wsClient *larkws.Client
	chatIDs  []string
	handlers map[string]CommandHandler
	cfg      config.FeishuConfig
}

// NewFeishuChannel 根据配置创建飞书渠道
func NewFeishuChannel(cfg config.FeishuConfig) (*FeishuChannel, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, fmt.Errorf("飞书配置缺少 app_id 或 app_secret")
	}
	chatIDs := make([]string, 0, len(cfg.ChatIDs)+1)
	seen := make(map[string]struct{}, len(cfg.ChatIDs)+1)
	appendChatID := func(v string) {
		id := strings.TrimSpace(v)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		chatIDs = append(chatIDs, id)
	}
	for _, id := range cfg.ChatIDs {
		appendChatID(id)
	}
	// 兼容旧配置：chat_id（单值）
	appendChatID(cfg.ChatID)
	if len(chatIDs) == 0 {
		logger.Warn("飞书渠道已启用但未配置 chat_ids/chat_id，消息不会推送")
	}

	sdkLogger := &feishuLogger{}

	// 创建飞书 API 客户端（自动管理 tenant_access_token）
	client := lark.NewClient(cfg.AppID, cfg.AppSecret,
		lark.WithLogLevel(larkcore.LogLevelInfo),
		lark.WithLogger(sdkLogger),
	)

	logger.Info("飞书 Bot 客户端已创建", "app_id", cfg.AppID)

	return &FeishuChannel{
		client:   client,
		chatIDs:  chatIDs,
		handlers: make(map[string]CommandHandler),
		cfg:      cfg,
	}, nil
}

func (f *FeishuChannel) Name() string { return "feishu" }

// Send 通过飞书 API 发送文本消息到指定的所有群聊
func (f *FeishuChannel) Send(text string) error {
	if f == nil || f.client == nil {
		return nil
	}
	if len(f.chatIDs) == 0 {
		return fmt.Errorf("飞书未配置 chat_ids/chat_id")
	}

	// 构建消息内容（飞书使用 JSON 格式）
	content, _ := json.Marshal(map[string]string{"text": text})

	var lastErr error
	for _, chatID := range f.chatIDs {
		req := larkim.NewCreateMessageReqBuilder().
			ReceiveIdType("chat_id").
			Body(larkim.NewCreateMessageReqBodyBuilder().
				ReceiveId(chatID).
				MsgType("text").
				Content(string(content)).
				Build()).
			Build()

		resp, err := f.client.Im.Message.Create(context.Background(), req)
		if err != nil {
			logger.Error("发送飞书消息失败", "chat_id", chatID, "err", err)
			lastErr = err
			continue
		}
		if !resp.Success() {
			logger.Error("发送飞书消息失败", "chat_id", chatID, "code", resp.Code, "msg", resp.Msg)
			lastErr = fmt.Errorf("飞书 API 错误 %d: %s", resp.Code, resp.Msg)
			continue
		}
	}

	return lastErr
}

func (f *FeishuChannel) RegisterCommand(cmd string, handler CommandHandler) {
	if f == nil {
		return
	}
	f.handlers[cmd] = handler
	logger.Info("注册飞书命令", "command", "/"+cmd)
}

// Start 通过 WebSocket 长连接启动命令监听（阻塞式）
func (f *FeishuChannel) Start() error {
	if f == nil || f.client == nil {
		return nil
	}

	// 创建事件分发器
	eventHandler := dispatcher.NewEventDispatcher("", "")

	// 注册消息接收事件
	eventHandler.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		f.handleMessageEvent(event)
		return nil
	})

	// 创建 WS 长连接客户端
	f.wsClient = larkws.NewClient(f.cfg.AppID, f.cfg.AppSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
		larkws.WithLogger(&feishuLogger{}),
	)

	logger.Info("飞书 Bot WebSocket 长连接启动中...")
	err := f.wsClient.Start(context.Background())
	if err != nil {
		logger.Error("飞书 Bot WebSocket 连接失败", "err", err)
	}
	return err
}

// feishuCommandContext 实现了 CommandContext 接口，允许异步回复飞书消息
type feishuCommandContext struct {
	channel *FeishuChannel
	msg     *larkim.EventMessage
}

func (c *feishuCommandContext) Reply(text string) {
	c.channel.replyToMessage(c.msg, text)
}

// handleMessageEvent 处理飞书消息事件，解析命令并调用 handler
func (f *FeishuChannel) handleMessageEvent(event *larkim.P2MessageReceiveV1) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return
	}

	msg := event.Event.Message
	msgType := msg.MessageType
	if msgType == nil || *msgType != "text" {
		return // 仅处理文本消息
	}

	// 解析消息内容（飞书文本消息格式：{"text":"内容"}）
	var textContent struct {
		Text string `json:"text"`
	}
	if msg.Content == nil {
		return
	}
	if err := json.Unmarshal([]byte(*msg.Content), &textContent); err != nil {
		return
	}

	text := strings.TrimSpace(textContent.Text)
	if !strings.HasPrefix(text, "/") {
		return // 不是命令消息
	}

	// 解析命令和参数
	parts := strings.Fields(text)
	command := strings.TrimPrefix(parts[0], "/")
	var args []string
	if len(parts) > 1 {
		args = parts[1:]
	}

	logger.Info("收到飞书命令", "command", command, "args", args)

	handler, ok := f.handlers[command]
	if !ok {
		f.replyToMessage(msg, unknownCommandReply(command))
		return
	}

	ctx := &feishuCommandContext{
		channel: f,
		msg:     msg,
	}

	response := handler(ctx, args)
	if response != "" {
		ctx.Reply(response)
	}
}

// replyToMessage 回复飞书消息（使用 reply API）
func (f *FeishuChannel) replyToMessage(msg *larkim.EventMessage, text string) {
	if msg.MessageId == nil {
		// 无法 reply 则直接发到群聊
		f.Send(text)
		return
	}

	content, _ := json.Marshal(map[string]string{"text": text})

	req := larkim.NewReplyMessageReqBuilder().
		MessageId(*msg.MessageId).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType("text").
			Content(string(content)).
			Build()).
		Build()

	resp, err := f.client.Im.Message.Reply(context.Background(), req)
	if err != nil {
		logger.Warn("飞书回复消息失败，尝试直接发送", "err", err)
		f.Send(text)
		return
	}
	if !resp.Success() {
		logger.Warn("飞书回复消息失败，尝试直接发送", "code", resp.Code, "msg", resp.Msg)
		f.Send(text)
	}
}

func (f *FeishuChannel) Close() error {
	if f == nil {
		return nil
	}
	// larkws.Client 没有显式的 close 方法，websocket 在 context cancel 时自动关闭
	logger.Info("飞书 Bot 已关闭")
	return nil
}
