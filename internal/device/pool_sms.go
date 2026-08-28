package device

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	qmicore "github.com/swordstudiox/vohive-plus/internal/qmi"
	"github.com/swordstudiox/vohive-plus/internal/smsnotify"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
	"github.com/swordstudiox/vohive-plus/pkg/smscodec"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

func (w *Worker) smsQMICore() qmiSMSCore {
	if w == nil {
		return nil
	}
	if w.qmiSMS != nil {
		return w.qmiSMS
	}
	return w.QMICore
}

func (w *Worker) qmiSMSStorageHasIndex(storage uint8, index uint32) bool {
	smsCore := w.smsQMICore()
	if smsCore == nil {
		return false
	}
	for _, tag := range []qmi.MessageTagType{qmi.TagTypeMTNotRead, qmi.TagTypeMTRead} {
		messages, err := smsCore.ListSMS(storage, tag)
		if err != nil {
			continue
		}
		for _, msg := range messages {
			if msg.Index == index {
				return true
			}
		}
	}
	return false
}

func (w *Worker) readIncomingSMSQMI(storage uint8, index uint32) (*qmimanager.DecodedSMS, uint8, error) {
	smsCore := w.smsQMICore()
	if smsCore == nil {
		return nil, storage, fmt.Errorf("qmi sms core not available")
	}

	if storage == 0 || storage == 1 {
		sms, err := smsCore.ReadSMS(storage, index)
		if err == nil {
			return sms, storage, nil
		}

		alternate := uint8(1)
		if storage == 1 {
			alternate = 0
		}
		if w.qmiSMSStorageHasIndex(alternate, index) {
			logger.Warn(fmt.Sprintf("[%s] 主存储读取失败，切换备用存储重试", w.ID),
				"index", index,
				"primary_storage", storage,
				"fallback_storage", alternate,
				"err", err,
			)
			sms, fallbackErr := smsCore.ReadSMS(alternate, index)
			if fallbackErr == nil {
				return sms, alternate, nil
			}
			return nil, alternate, fallbackErr
		}
		return nil, storage, err
	}

	for _, candidate := range []uint8{1, 0} {
		sms, err := smsCore.ReadSMS(candidate, index)
		if err == nil {
			return sms, candidate, nil
		}
	}

	return nil, storage, fmt.Errorf("QMI 短信读取失败: index=%d", index)
}

func (w *Worker) CheckAllSMSQMI() error {
	smsCore := w.smsQMICore()
	if smsCore == nil {
		return fmt.Errorf("qmi sms core not available")
	}

	type qmiStoredSMS struct {
		storage uint8
		index   uint32
	}

	seen := make(map[string]struct{})
	discovered := make([]qmiStoredSMS, 0)
	readResiduals := make([]qmiStoredSMS, 0)
	var errs []error
	successes := 0

	for _, storage := range []uint8{0, 1} {
		messages, err := smsCore.ListSMS(storage, qmi.TagTypeMTNotRead)
		if err != nil {
			errs = append(errs, fmt.Errorf("storage %d: %w", storage, err))
			continue
		}
		successes++
		for _, msg := range messages {
			key := fmt.Sprintf("%d:%d", storage, msg.Index)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			discovered = append(discovered, qmiStoredSMS{storage: storage, index: msg.Index})
		}

		messages, err = smsCore.ListSMS(storage, qmi.TagTypeMTRead)
		if err != nil {
			errs = append(errs, fmt.Errorf("storage %d read: %w", storage, err))
			continue
		}
		successes++
		for _, msg := range messages {
			key := fmt.Sprintf("%d:%d", storage, msg.Index)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			readResiduals = append(readResiduals, qmiStoredSMS{storage: storage, index: msg.Index})
		}
	}

	if successes == 0 && len(errs) > 0 {
		return errors.Join(errs...)
	}
	if len(discovered) == 0 && len(readResiduals) == 0 {
		return nil
	}

	if len(discovered) > 0 {
		logger.Info(fmt.Sprintf("[%s] QMI 轮询发现 %d 条未读短信", w.ID, len(discovered)))
	}
	for _, msg := range discovered {
		w.handleNewSMSQMI(msg.storage, msg.index)
	}
	if len(readResiduals) > 0 {
		logger.Info(fmt.Sprintf("[%s] QMI 轮询清理 %d 条已读残留短信", w.ID, len(readResiduals)))
	}
	for _, msg := range readResiduals {
		if err := smsCore.WMSDeleteMessage(context.Background(), msg.storage, msg.index); err != nil {
			logger.Warn(fmt.Sprintf("[%s] 清理已读残留短信失败 (QMI)", w.ID), "index", msg.index, "storage", msg.storage, "err", err)
		}
	}
	return nil
}

func (w *Worker) handleNewSMSQMI(storage uint8, index uint32) {
	logger.Info(fmt.Sprintf("[%s] 收到新短信通知 (QMI)", w.ID), "index", index, "storage", storage)

	sms, actualStorage, err := w.readIncomingSMSQMI(storage, index)
	if err != nil {
		logger.Error(fmt.Sprintf("[%s] 读取短信失败 (QMI)", w.ID), "index", index, "storage", storage, "err", err)
		return
	}

	w.processDecodedSMSQMI(sms)

	if smsCore := w.smsQMICore(); smsCore != nil {
		if err := smsCore.WMSDeleteMessage(context.Background(), actualStorage, index); err != nil {
			logger.Warn(fmt.Sprintf("[%s] 删除短信失败 (QMI)", w.ID), "index", index, "storage", actualStorage, "err", err)
		}
		return
	}
	if w.Backend != nil {
		if err := w.Backend.DeleteSMS(context.Background(), int(index)); err != nil {
			logger.Warn(fmt.Sprintf("[%s] 删除短信失败 (Backend)", w.ID), "index", index, "err", err)
		}
	} else if w.Modem != nil {
		if err := w.Modem.DeleteSMS(index); err != nil {
			logger.Warn(fmt.Sprintf("[%s] 删除短信失败", w.ID), "index", index, "err", err)
		}
	}
}

func (w *Worker) handleNewSMSRawQMI(info qmicore.RawSMSIndication) {
	logger.Info(fmt.Sprintf("[%s] 收到新短信通知 (QMI Raw)", w.ID),
		"pdu_len", len(info.PDU),
		"ack_required", info.AckRequired,
		"transaction_id", info.TransactionID,
		"format", info.Format,
	)

	sms, err := qmimanager.DecodeIncomingSMSPDU(info.PDU, qmiSMSStorageUnknown, ^uint32(0))
	if err != nil {
		logger.Error(fmt.Sprintf("[%s] 解析原始短信失败 (QMI)", w.ID),
			"pdu_len", len(info.PDU),
			"pdu_hex", rawSMSPDUHexForLog(info.PDU),
			"ack_required", info.AckRequired,
			"transaction_id", info.TransactionID,
			"format", info.Format,
			"err", err,
		)
		w.ackRawSMSQMI(info, "decode_failure")
		return
	}

	w.processDecodedSMSQMI(sms)
	w.ackRawSMSQMI(info, "processed")
}

func (w *Worker) ackRawSMSQMI(info qmicore.RawSMSIndication, reason string) {
	if !info.AckRequired {
		return
	}
	smsCore := w.smsQMICore()
	if smsCore == nil {
		logger.Warn(fmt.Sprintf("[%s] QMI 原始短信需要 ACK 但 SMS Core 不可用", w.ID),
			"transaction_id", info.TransactionID,
			"reason", reason,
		)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := smsCore.AckRawSMS(ctx, info, true); err != nil {
		logger.Warn(fmt.Sprintf("[%s] QMI 原始短信 ACK 失败", w.ID),
			"transaction_id", info.TransactionID,
			"reason", reason,
			"err", err,
		)
	}
}

func rawSMSPDUHexForLog(pdu []byte) string {
	const maxRawSMSLogBytes = 128
	if len(pdu) <= maxRawSMSLogBytes {
		return strings.ToUpper(hex.EncodeToString(pdu))
	}
	return strings.ToUpper(hex.EncodeToString(pdu[:maxRawSMSLogBytes])) + "...(truncated)"
}

func (w *Worker) processDecodedSMSQMI(sms *qmimanager.DecodedSMS) {
	if sms == nil {
		return
	}

	if sms.IsConcat {
		concat := smscodec.ConcatInfo{
			IsConcat: true,
			Ref:      sms.ConcatRef,
			Total:    sms.ConcatTotal,
			Seq:      sms.ConcatSeq,
		}
		logger.Debug(fmt.Sprintf("[%s] 收到 QMI 短信分片", w.ID), "ref", sms.ConcatRef, "seq", sms.ConcatSeq, "total", sms.ConcatTotal)
		complete, full := w.reassembler.Add(sms.Sender, concat, sms.Message)
		if !complete {
			return
		}
		sms.Message = full
		logger.Info(fmt.Sprintf("[%s] QMI 长短信重组完成", w.ID), "total", sms.ConcatTotal)
	}

	w.processSMS(sms.Sender, sms.Message, sms.Timestamp)
}

func (w *Worker) cleanupFragmentCache(ttl time.Duration) {
	if w == nil || w.reassembler == nil {
		return
	}
	w.reassembler.Cleanup(ttl)
}

func (w *Worker) getIMEI() string {
	return w.getIMEIWithContext(context.Background())
}

func (w *Worker) getIMEIWithContext(ctx context.Context) string {
	if ctx == nil {
		ctx = context.Background()
	}
	if w.Backend != nil {
		if v, err := w.Backend.GetIMEI(ctx); err == nil && v != "" {
			return v
		}
		if w.Backend.Mode() != "at" {
			return ""
		}
	}
	if w.Modem != nil {
		return w.Modem.GetIMEI()
	}
	return ""
}

func (w *Worker) getSMSCWithContext(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if w == nil {
		return "", fmt.Errorf("worker 为空")
	}
	configured := strings.TrimSpace(w.Config.SMSC)
	if w.Backend != nil {
		if provider, ok := w.Backend.(backend.SMSCProvider); ok {
			v, err := provider.GetSMSC(ctx)
			v = strings.TrimSpace(v)
			if v != "" || configured == "" {
				return v, err
			}
			return configured, nil
		}
		if configured != "" {
			return configured, nil
		}
		if w.Backend.Mode() != backend.BackendAT {
			return "", fmt.Errorf("backend=%s 未实现 SMSCProvider", w.Backend.Mode())
		}
	}
	if w.Modem != nil {
		v, err := w.Modem.QuerySMSC()
		v = strings.TrimSpace(v)
		if v != "" || configured == "" {
			return v, err
		}
		return configured, nil
	}
	return configured, nil
}

func (w *Worker) getPhoneNumberWithContext(ctx context.Context) string {
	if ctx == nil {
		ctx = context.Background()
	}
	if w == nil {
		return ""
	}
	if w.Backend != nil {
		if v, err := w.Backend.GetMSISDN(ctx); err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		if w.Backend.Mode() != backend.BackendAT {
			return ""
		}
	}
	if w.Modem != nil {
		if v, err := w.Modem.QueryMSISDN(); err == nil {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (w *Worker) SendSMS(phone, message string) error {
	return w.SendSMSWithOptions(phone, message, smscodec.SubmitOptions{})
}

func (w *Worker) SendSMSWithOptions(phone, message string, opts smscodec.SubmitOptions) error {
	if w.Backend != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		if sender, ok := w.Backend.(interface {
			SendSMSWithOptions(context.Context, string, string, smscodec.SubmitOptions) error
		}); ok {
			return sender.SendSMSWithOptions(ctx, phone, message, opts)
		}
		if encoding, _ := smscodec.NormalizeSMSEncoding(string(opts.Encoding)); encoding != smscodec.SMSEncodingAuto {
			return fmt.Errorf("设备 %s 的短信后端不支持编码选项: %s", w.ID, opts.Encoding)
		}
		return w.Backend.SendSMS(ctx, phone, message)
	}
	if w.Modem != nil {
		return w.Modem.SendSMSWithOptions(phone, message, opts)
	}
	return fmt.Errorf("设备 %s 无可用的短信发送后端", w.ID)
}

func (w *Worker) GetIMSI() string {
	if w.Backend != nil {
		if v, err := w.Backend.GetIMSI(context.Background()); err == nil && v != "" {
			return v
		}
		if w.Backend.Mode() != "at" {
			return ""
		}
	}
	if w.Modem != nil {
		return w.Modem.GetIMSI()
	}
	return ""
}

func (w *Worker) GetDeviceStatus() modem.DeviceStatus {
	_ = w.RefreshRuntime(nil, "get_device_status")
	return w.ProjectDeviceStatus()
}

func (w *Worker) IsDeviceHealthy() bool {
	healthy, _ := w.ProbeDeviceHealth()
	return healthy
}

func (w *Worker) ProbeDeviceHealth() (bool, error) {
	if w.Backend != nil && w.Backend.Mode() != "at" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := w.Backend.GetOperatingMode(ctx)
		if err != nil {
			logger.Debug(fmt.Sprintf("[%s] 控制面健康探测失败", w.ID),
				"event", "control_health_check_failed",
				"backend", w.Backend.Mode(),
				"recovery_remaining_ms", w.healthRecoveryRemaining(time.Now()).Milliseconds(),
				"err", err)
			return false, err
		}
		return true, nil
	}
	if w.Modem != nil {
		return w.Modem.IsHealthy(), nil
	}
	return false, nil
}

func (w *Worker) processSMS(sender, content string, timestamp time.Time) {
	logger.Info(fmt.Sprintf("[%s] 处理新短信", w.ID), "sender", sender, "content_len", len(content))

	if smsnotify.ShouldSuppressReceivedSMS(content) {
		logger.Info(fmt.Sprintf("[%s] 短信已过滤（运营商 OTA/不可解码二进制包）", w.ID), "sender", sender)
		return
	}

	imsi := w.GetCachedIMSI()
	if imsi == "" {
		if w.Backend != nil {
			if v, err := w.Backend.GetIMSI(context.Background()); err == nil {
				imsi = v
			}
		}
		if imsi == "" && w.Modem != nil {
			imsi = w.Modem.GetIMSI()
		}
	}
	if imsi != "" {
		if err := db.SaveSMS(imsi, sender, "", content, 1, 0, timestamp); err != nil {
			logger.Warn(fmt.Sprintf("[%s] 保存短信到数据库失败", w.ID), "err", err)
		}
	}

	if notifier := w.Pool.getNotifier(); notifier != nil {
		notifier.NotifySMS(w.ID, sender, content, timestamp)
	}
}
