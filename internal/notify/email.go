package notify

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type EmailChannel struct {
	cfg config.EmailConfig
}

func NewEmailChannel(cfg config.EmailConfig) (*EmailChannel, error) {
	if cfg.SMTPHost == "" || cfg.SMTPPort == 0 || cfg.FromAddress == "" || len(cfg.ToAddresses) == 0 {
		return nil, errors.New("email configuration is incomplete")
	}
	return &EmailChannel{cfg: cfg}, nil
}

func (c *EmailChannel) Name() string {
	return "email"
}

func (c *EmailChannel) Send(text string) error {
	return c.SendWithContext(NotificationContext{Event: "通知", Text: text})
}

func (c *EmailChannel) SendWithContext(ctx NotificationContext) error {
	auth := smtp.PlainAuth("", c.cfg.Username, c.cfg.Password, c.cfg.SMTPHost)
	addr := fmt.Sprintf("%s:%d", c.cfg.SMTPHost, c.cfg.SMTPPort)

	subject := fmt.Sprintf("[Vohive] %s", ctx.Event)
	if label := ctx.DeviceLabel(); label != "未知设备" {
		subject = fmt.Sprintf("[Vohive] %s - %s", ctx.Event, label)
	}

	to := strings.Join(c.cfg.ToAddresses, ",")
	msg := []byte(fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", c.cfg.FromAddress, to, subject, ctx.Text))

	if c.cfg.UseSSL {
		tlsconfig := &tls.Config{
			ServerName: c.cfg.SMTPHost,
		}
		conn, err := tls.Dial("tcp", addr, tlsconfig)
		if err != nil {
			logger.Warn("邮件 SSL/TLS 连接失败", "err", err, "host", c.cfg.SMTPHost)
			return err
		}
		defer conn.Close()

		client, err := smtp.NewClient(conn, c.cfg.SMTPHost)
		if err != nil {
			logger.Warn("创建 SMTP 客户端失败", "err", err)
			return err
		}
		defer client.Close()

		if ok, _ := client.Extension("AUTH"); ok {
			if err = client.Auth(auth); err != nil {
				logger.Warn("SMTP 认证失败", "err", err)
				return err
			}
		}

		if err = client.Mail(c.cfg.FromAddress); err != nil {
			return err
		}
		for _, a := range c.cfg.ToAddresses {
			if err = client.Rcpt(a); err != nil {
				return err
			}
		}
		w, err := client.Data()
		if err != nil {
			return err
		}
		if _, err = w.Write(msg); err != nil {
			return err
		}
		if err = w.Close(); err != nil {
			return err
		}
		client.Quit()
	} else {
		err := smtp.SendMail(addr, auth, c.cfg.FromAddress, c.cfg.ToAddresses, msg)
		if err != nil {
			logger.Warn("邮件发送失败", "err", err, "host", c.cfg.SMTPHost)
			return err
		}
	}

	return nil
}

func (c *EmailChannel) RegisterCommand(cmd string, handler CommandHandler) {
	// 邮件渠道不支持接收指令
}

func (c *EmailChannel) Start() error {
	return nil
}

func (c *EmailChannel) Close() error {
	return nil
}
