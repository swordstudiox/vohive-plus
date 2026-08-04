package device

import (
	"context"
	"strings"

	"github.com/iniwex5/vowifi-go/runtimehost/eventhost"
	"github.com/swordstudiox/vohive-plus/internal/smsnotify"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type poolVoWiFiRuntimeDispatcher struct {
	pool *Pool
}

func isCompatVoWiFiIncomingSMSLog(msg string) bool {
	return strings.HasPrefix(strings.TrimSpace(msg), "收到新短信 / VoWiFi\n")
}

func (d poolVoWiFiRuntimeDispatcher) Dispatch(ctx context.Context, e eventhost.Event) {
	if d.pool == nil || e == nil {
		return
	}
	recorder := vowifiSMSHistoryRecorder{pool: d.pool}
	var recordResult vowifiSMSRecordResult
	switch v := e.(type) {
	case eventhost.SMSReceived:
		res, err := recorder.RecordReceived(v)
		if err != nil {
			logger.Warn("VoWiFi 上层入库入站短信失败", "device", v.DevID, "sender", v.Sender, "err", err)
		}
		recordResult = res
	case eventhost.SMSSent:
		if err := recorder.RecordSent(v); err != nil {
			logger.Warn("VoWiFi 上层入库出站短信失败", "device", v.DevID, "to", v.TargetURI, "err", err)
		}
	case eventhost.LocalNumberLearned:
		if err := recorder.RecordLocalNumberLearned(v); err != nil {
			logger.Warn("VoWiFi 上层持久化本机号码失败", "device", v.DevID, "imsi", v.IMSI, "phone", v.Number, "err", err)
		}
	}

	notifier := d.pool.getNotifier()
	if notifier == nil {
		return
	}

	if sms, ok := e.(eventhost.SMSReceived); ok {
		if smsnotify.ShouldSuppressReceivedSMS(sms.Content) {
			logger.Info("VoWiFi 短信已过滤（运营商 OTA/无法解码二进制包），不进行通知推送", "device", sms.DevID, "sender", sms.Sender)
			return
		}
		if recordResult.Duplicate {
			logger.Info("VoWiFi 短信重复（通过数据库去重兜底），不进行重复通知推送", "device", sms.DevID, "sender", sms.Sender)
			return
		}
		if withSource, ok := notifier.(SMSSourceNotifier); ok {
			withSource.NotifySMSWithSource(sms.DevID, sms.Sender, sms.Content, "VoWiFi", sms.Time)
		} else {
			notifier.NotifySMS(sms.DevID, sms.Sender, sms.Content, sms.Time)
		}
	}

	logNotify, ok := e.(eventhost.LogNotify)
	if !ok || strings.TrimSpace(logNotify.Message) == "" || isCompatVoWiFiIncomingSMSLog(logNotify.Message) {
		return
	}
	notifier.NotifyRaw(logNotify.Message)
}
