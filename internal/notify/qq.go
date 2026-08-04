package notify

import (
	"context"
	"strings"
	"sync"

	qqbot "github.com/iniwex5/qqbot"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type qqApp interface {
	Send(ctx context.Context, delivery qqbot.Delivery) (qqbot.Receipt, error)
	Command(name string, handler qqbot.CommandHandler)
	OnText(handler qqbot.TextHandler)
	Run(ctx context.Context) error
	Close() error
}

// QQChannel 实现 Channel 接口的 QQ 通知渠道
// 使用 OpenID 白名单鉴权，只对配置中指定的会话进行回复和推送
type QQChannel struct {
	app qqApp

	mu                sync.RWMutex
	allowedRecipients map[string]qqbot.Recipient // 从配置解析的 OpenID 白名单
}

// NewQQChannel 根据配置创建 QQ 渠道
// defaultKind 固定为 PlainText，commandPrefix 固定为 "/"，与 TG Bot 统一
func NewQQChannel(cfg config.QQConfig) (*QQChannel, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	// 解析 OpenID 白名单
	allowed := parseAllowedRecipients(cfg.GroupIDs, cfg.DirectIDs)

	channel := &QQChannel{
		allowedRecipients: allowed,
	}

	app, err := qqbot.New(qqbot.Settings{
		AppID:       strings.TrimSpace(cfg.AppID),
		AppSecret:   strings.TrimSpace(cfg.AppSecret),
		DefaultKind: qqbot.PlainText, // 固定为纯文本
	}, qqbot.WithPrefix("/"), // 固定为 /，与 TG Bot 统一
		qqbot.WithUnknownCommand(func(ctx context.Context, c qqbot.Conversation, _ qqbot.ParsedCommand) error {
			channel.logIncoming(c.Incoming())
			if !channel.isAllowed(c.Incoming()) {
				return nil
			}
			_, err := c.RespondText(ctx, "未知命令")
			return err
		}))
	if err != nil {
		return nil, err
	}

	channel.app = app

	// 所有文本消息都记录日志，但只对白名单内的会话进行响应
	channel.app.OnText(func(ctx context.Context, c qqbot.Conversation) error {
		channel.logIncoming(c.Incoming())
		return nil
	})

	if len(allowed) > 0 {
		logger.Info("QQ Bot OpenID 白名单已加载", "count", len(allowed))
	} else {
		logger.Warn("QQ Bot 未配置 OpenID 白名单，将不会回复任何会话，也不会推送消息。请在设置中配置 open_id")
	}

	return channel, nil
}

func (q *QQChannel) Name() string { return "qq" }

// Send 向白名单中的所有 recipient 推送消息
func (q *QQChannel) Send(text string) error {
	if q == nil || q.app == nil {
		return nil
	}

	recipients := q.snapshotAllowed()
	if len(recipients) == 0 {
		logger.Debug("QQ 渠道无白名单 recipient，跳过推送")
		return nil
	}

	var lastErr error
	for _, recipient := range recipients {
		_, err := q.app.Send(context.Background(), qqbot.Delivery{
			To:   recipient,
			Kind: qqbot.PlainText,
			Body: text,
		})
		if err != nil {
			lastErr = err
			logger.Warn("发送 QQ 消息失败", "recipient", recipient.ID, "kind", recipient.Kind, "err", err)
		}
	}
	return lastErr
}

// RegisterCommand 注册命令处理器，内部自动添加白名单鉴权
func (q *QQChannel) RegisterCommand(cmd string, handler CommandHandler) {
	if q == nil || q.app == nil || handler == nil {
		return
	}

	name := strings.ToLower(strings.TrimSpace(cmd))
	if name == "" {
		return
	}

	q.app.Command(name, func(ctx context.Context, c qqbot.Conversation, parsed qqbot.ParsedCommand) error {
		q.logIncoming(c.Incoming())

		// 白名单鉴权
		if !q.isAllowed(c.Incoming()) {
			return nil
		}

		cmdCtx := &qqCommandContext{conversation: c}
		response := handler(cmdCtx, append([]string(nil), parsed.Params...))
		if response == "" {
			return nil
		}
		_, err := c.RespondText(ctx, response)
		return err
	})
	logger.Info("注册 QQ 命令", "command", "/"+name)
}

func (q *QQChannel) Start() error {
	if q == nil || q.app == nil {
		return nil
	}
	logger.Info("QQ Bot 命令监听已启动")
	return q.app.Run(context.Background())
}

func (q *QQChannel) Close() error {
	if q == nil || q.app == nil {
		return nil
	}
	return q.app.Close()
}

// logIncoming 记录所有收到的消息到日志（无论是否在白名单中）
// 用户可以从日志中获取 OpenID，然后配置到设置中
func (q *QQChannel) logIncoming(incoming qqbot.Incoming) {
	logger.Info("QQ Bot 收到消息",
		"kind", string(incoming.Kind),
		"from_openid", incoming.From,
		"to_openid", incoming.To.ID,
		"to_kind", string(incoming.To.Kind),
		"text", incoming.Text,
	)
}

// isAllowed 检查消息的目标 OpenID 是否在白名单中
func (q *QQChannel) isAllowed(incoming qqbot.Incoming) bool {
	if strings.TrimSpace(incoming.To.ID) == "" {
		return false
	}
	key := string(incoming.To.Kind) + ":" + incoming.To.ID
	q.mu.RLock()
	defer q.mu.RUnlock()
	_, ok := q.allowedRecipients[key]
	return ok
}

// snapshotAllowed 快照白名单中的所有 recipient
func (q *QQChannel) snapshotAllowed() []qqbot.Recipient {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]qqbot.Recipient, 0, len(q.allowedRecipients))
	for _, recipient := range q.allowedRecipients {
		out = append(out, recipient)
	}
	return out
}

// parseAllowedRecipients 解析群组和私聊的白名单 IDs
func parseAllowedRecipients(groupIDs, directIDs string) map[string]qqbot.Recipient {
	result := make(map[string]qqbot.Recipient)

	// 解析群组 ID
	for _, id := range strings.Split(groupIDs, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := "group:" + id
		result[key] = qqbot.Recipient{
			Kind: qqbot.GroupRecipient,
			ID:   id,
		}
		logger.Info("QQ Bot 添加群组白名单", "openid", id)
	}

	// 解析私聊 ID
	for _, id := range strings.Split(directIDs, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := "direct:" + id
		result[key] = qqbot.Recipient{
			Kind: qqbot.DirectRecipient,
			ID:   id,
		}
		logger.Info("QQ Bot 添加私聊白名单", "openid", id)
	}

	return result
}

type qqCommandContext struct {
	conversation qqbot.Conversation
}

func (c *qqCommandContext) Reply(text string) {
	if c == nil || c.conversation == nil {
		return
	}
	go func() {
		if _, err := c.conversation.RespondText(context.Background(), text); err != nil {
			logger.Warn("回复 QQ 命令消息失败", "err", err)
		}
	}()
}
