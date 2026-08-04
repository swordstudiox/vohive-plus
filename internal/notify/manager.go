package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/device"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

// Manager 统一通知管理器
// 持有多个 Channel 实例，向所有已启用渠道广播通知和命令
type Manager struct {
	pool     *device.Pool
	channels []Channel // 所有已启用的通知渠道
}

type NotificationContext struct {
	Event      string
	Text       string
	DeviceID   string
	DeviceName string
	Timestamp  time.Time
}

func (c NotificationContext) DeviceLabel() string {
	id := strings.TrimSpace(c.DeviceID)
	name := strings.TrimSpace(c.DeviceName)
	if name != "" && id != "" {
		return fmt.Sprintf("%s (%s)", name, id)
	}
	if name != "" {
		return name
	}
	if id != "" {
		return id
	}
	return "未知设备"
}

type contextualChannel interface {
	SendWithContext(ctx NotificationContext) error
}

// NewManager 根据配置创建通知管理器，初始化所有已启用的通知渠道
func NewManager(cfg *config.Config, pool *device.Pool) (*Manager, error) {
	m := &Manager{
		pool: pool,
	}

	// 初始化所有通知渠道
	if err := m.initChannels(cfg); err != nil {
		return nil, err
	}

	return m, nil
}

// initChannels 根据配置创建并启动所有通知渠道
func (m *Manager) initChannels(cfg *config.Config) error {
	m.channels = nil

	// Telegram 渠道
	if cfg.Telegram.Enabled {
		tg, err := NewTelegramChannel(cfg.Telegram)
		if err != nil {
			logger.Error("初始化 Telegram 渠道失败", "err", err)
			return err
		}
		if tg != nil {
			m.channels = append(m.channels, tg)
		}
	}

	// 飞书渠道
	if cfg.Feishu.Enabled {
		fs, err := NewFeishuChannel(cfg.Feishu)
		if err != nil {
			logger.Error("初始化飞书渠道失败", "err", err)
			return err
		}
		if fs != nil {
			m.channels = append(m.channels, fs)
		}
	}

	// QQ 渠道
	if cfg.QQ.Enabled {
		qq, err := NewQQChannel(cfg.QQ)
		if err != nil {
			logger.Error("初始化 QQ 渠道失败", "err", err)
			return err
		}
		if qq != nil {
			m.channels = append(m.channels, qq)
		}
	}

	// Webhook 渠道
	if cfg.Webhook.Enabled {
		wh, err := NewWebhookChannel(cfg.Webhook)
		if err != nil {
			logger.Error("初始化 Webhook 渠道失败", "err", err)
			return err
		}
		if wh != nil {
			m.channels = append(m.channels, wh)
		}
	}

	// Bark 渠道
	if cfg.Bark.Enabled {
		bk, err := NewBarkChannel(cfg.Bark)
		if err != nil {
			logger.Error("初始化 Bark 渠道失败", "err", err)
			return err
		}
		if bk != nil {
			m.channels = append(m.channels, bk)
		}
	}

	// Email 渠道
	if cfg.Email.Enabled {
		em, err := NewEmailChannel(cfg.Email)
		if err != nil {
			logger.Error("初始化 Email 渠道失败", "err", err)
			return err
		}
		if em != nil {
			m.channels = append(m.channels, em)
		}
	}

	// Pushplus 渠道
	if cfg.Pushplus.Enabled {
		pp, err := NewPushplusChannel(cfg.Pushplus)
		if err != nil {
			logger.Error("初始化 Pushplus 渠道失败", "err", err)
			return err
		}
		if pp != nil {
			m.channels = append(m.channels, pp)
		}
	}

	// 向所有渠道注册命令
	m.registerCommands()

	// 启动所有渠道的命令监听
	for _, ch := range m.channels {
		ch := ch
		go func() {
			if err := ch.Start(); err != nil {
				logger.Error("通知渠道命令监听失败", "channel", ch.Name(), "err", err)
			}
		}()
	}

	return nil
}

// registerCommands 向所有已启用渠道注册同一组命令处理器
func (m *Manager) registerCommands() {
	commands := map[string]CommandHandler{
		"send":   m.handleCmdSendSMS,
		"status": m.handleCmdStatus,
		"rotate": m.handleCmdRotate,
		"list":   m.handleCmdList,
		"sms":    m.handleCmdSMSInbox,
		"esim":   m.handleCmdEsim,
		"switch": m.handleCmdSwitch,
		"vocall": m.handleCmdCall,
	}

	for _, ch := range m.channels {
		for cmd, handler := range commands {
			ch.RegisterCommand(cmd, handler)
		}
	}
}

// Close 关闭所有通知渠道
func (m *Manager) Close() {
	for _, ch := range m.channels {
		_ = ch.Close()
	}
}

// UpdateConfig 重新加载通知配置（热更新）
func (m *Manager) UpdateConfig(cfg *config.Config) error {
	// 关闭现有渠道
	m.Close()
	m.channels = nil

	// 重新初始化所有渠道
	return m.initChannels(cfg)
}

// NotifySMS 实现 device.Notifier 接口 — 收到短信通知
func (m *Manager) NotifySMS(deviceID, sender, content string, timestamp time.Time) {
	m.NotifySMSWithSource(deviceID, sender, content, "蜂窝", timestamp)
}

func (m *Manager) NotifySMSWithSource(deviceID, sender, content, source string, timestamp time.Time) {
	source = strings.TrimSpace(source)
	if source == "" {
		source = "蜂窝"
	}
	msg := fmt.Sprintf("收到新短信 / %s\n设备  %s\n号码  %s\n时间  %s\n内容  %s",
		source, deviceID, sender, timestamp.Format("2006-01-02 15:04:05"), content)

	logger.Info("开始发送短信通知",
		"event", "sms_received",
		"sms_device", deviceID,
		"source", source,
		"channel_count", len(m.channels))

	m.broadcastWithContext(NotificationContext{
		Event:      "sms_received",
		Text:       msg,
		DeviceID:   deviceID,
		DeviceName: m.resolveDeviceName(deviceID),
		Timestamp:  timestamp,
	})
}

// NotifyRaw 发送原始文本通知到所有渠道
func (m *Manager) NotifyRaw(msg string) {
	m.broadcastWithContext(NotificationContext{
		Event:     "raw",
		Text:      msg,
		Timestamp: time.Now(),
	})
}

// NotifyIPRotated 实现 device.Notifier 接口 — IP 切换通知
func (m *Manager) NotifyIPRotated(deviceID, oldIP, newIP string, duration time.Duration) {
	displayName := deviceID
	if m.pool != nil {
		if worker := m.pool.GetWorker(deviceID); worker != nil && worker.Config.Name != "" {
			displayName = fmt.Sprintf("%s (%s)", worker.Config.Name, deviceID)
		}
	}
	msg := fmt.Sprintf("公网切换 / 完成\n设备    %s\n旧 IP   %s\n新 IP   %s\n耗时    %s", displayName, oldIP, newIP, duration.String())
	m.broadcastWithContext(NotificationContext{
		Event:      "ip_rotated",
		Text:       msg,
		DeviceID:   deviceID,
		DeviceName: m.resolveDeviceName(deviceID),
		Timestamp:  time.Now(),
	})
}

// NotifyIncomingCall 实现 voice.CallNotifier 接口 — 来电通知
func (m *Manager) NotifyIncomingCall(deviceID, caller, callee string) {
	if len(m.channels) == 0 {
		return
	}

	msg := fmt.Sprintf("来电通知\n设备    %s\n主叫    %s\n被叫    %s",
		deviceID, caller, callee)

	logger.Info("开始发送来电通知", "device", deviceID, "caller", caller, "channel_count", len(m.channels))

	m.broadcastWithContext(NotificationContext{
		Event:      "incoming_call",
		Text:       msg,
		DeviceID:   deviceID,
		DeviceName: m.resolveDeviceName(deviceID),
		Timestamp:  time.Now(),
	})
}

func (m *Manager) resolveDeviceName(deviceID string) string {
	if strings.TrimSpace(deviceID) == "" || m.pool == nil {
		return ""
	}
	worker := m.pool.GetWorker(deviceID)
	if worker == nil {
		return ""
	}
	return strings.TrimSpace(worker.Config.Name)
}

func (m *Manager) broadcastWithContext(ctx NotificationContext) {
	ctx.Text = strings.TrimSpace(ctx.Text)
	if ctx.Text == "" {
		return
	}
	if ctx.Timestamp.IsZero() {
		ctx.Timestamp = time.Now()
	}
	if strings.TrimSpace(ctx.Event) == "" {
		ctx.Event = "notification"
	}

	for _, ch := range m.channels {
		ch := ch // capture variable
		go func() {
			if withCtx, ok := ch.(contextualChannel); ok {
				if err := withCtx.SendWithContext(ctx); err != nil {
					logger.Warn("通知渠道发送失败", "channel", ch.Name(), "event", ctx.Event, "err", err)
				}
				return
			}
			if err := ch.Send(ctx.Text); err != nil {
				logger.Warn("通知渠道发送失败", "channel", ch.Name(), "event", ctx.Event, "err", err)
			}
		}()
	}
}

// GetChannelNames 返回所有已启用渠道的名称列表
func (m *Manager) GetChannelNames() []string {
	names := make([]string, 0, len(m.channels))
	for _, ch := range m.channels {
		names = append(names, ch.Name())
	}
	return names
}
