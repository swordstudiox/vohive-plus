package device

import (
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/eventhost"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/smsnotify"
)

const vowifiReceivedSMSDuplicateWindow = 30 * time.Minute

type vowifiSMSHistoryRecorder struct {
	pool *Pool
}

type vowifiSMSRecordResult struct {
	Stored     bool
	Duplicate  bool
	Suppressed bool
}

func RecordVoWiFiSMSSendFailure(p *Pool, deviceID, target, content string, at time.Time) error {
	return vowifiSMSHistoryRecorder{pool: p}.RecordSendFailure(deviceID, target, content, at)
}

func (r vowifiSMSHistoryRecorder) resolveIMSI(devID, fallbackIMSI string) string {
	imsi := strings.TrimSpace(fallbackIMSI)
	if imsi != "" {
		return imsi
	}
	if r.pool == nil {
		return ""
	}
	w := r.pool.GetWorker(strings.TrimSpace(devID))
	if w == nil {
		return ""
	}
	if cached := strings.TrimSpace(w.GetCachedIMSI()); cached != "" {
		return cached
	}
	return strings.TrimSpace(w.GetIMSI())
}

func (r vowifiSMSHistoryRecorder) resolveICCID(devID string) string {
	if r.pool == nil {
		return ""
	}
	w := r.pool.GetWorker(strings.TrimSpace(devID))
	if w == nil {
		return ""
	}
	return strings.TrimSpace(w.GetCachedDeviceStatus().ICCID)
}

func (r vowifiSMSHistoryRecorder) localPhone(imsi string) string {
	phone, _ := db.GetSIMCardPhoneNumberByIMSI(strings.TrimSpace(imsi))
	return strings.TrimSpace(phone)
}

func (r vowifiSMSHistoryRecorder) eventTime(at time.Time) time.Time {
	if at.IsZero() {
		return time.Now()
	}
	return at
}

func (r vowifiSMSHistoryRecorder) RecordReceived(e eventhost.SMSReceived) (vowifiSMSRecordResult, error) {
	if smsnotify.ShouldSuppressReceivedSMS(e.Content) {
		return vowifiSMSRecordResult{Suppressed: true}, nil
	}
	imsi := r.resolveIMSI(e.DevID, "")
	if imsi == "" {
		return vowifiSMSRecordResult{}, nil
	}
	localPhone := r.localPhone(imsi)
	ts := r.eventTime(e.Time)

	dup, err := db.HasDuplicateReceivedSMS(imsi, localPhone, e.Sender, localPhone, e.Content, ts, vowifiReceivedSMSDuplicateWindow)
	if err != nil {
		return vowifiSMSRecordResult{}, err
	}
	if dup {
		return vowifiSMSRecordResult{Duplicate: true}, nil
	}

	err = db.SaveSMSWithLocalPhone(imsi, localPhone, strings.TrimSpace(e.Sender), localPhone, e.Content, 1, 0, ts)
	if err != nil {
		return vowifiSMSRecordResult{}, err
	}
	return vowifiSMSRecordResult{Stored: true}, nil
}

func (r vowifiSMSHistoryRecorder) RecordSent(e eventhost.SMSSent) error {
	imsi := r.resolveIMSI(e.DevID, "")
	if imsi == "" {
		return nil
	}
	localPhone := r.localPhone(imsi)
	return db.SaveSMSWithLocalPhone(imsi, localPhone, localPhone, strings.TrimSpace(e.TargetURI), e.Content, 2, 2, r.eventTime(e.Time))
}

func (r vowifiSMSHistoryRecorder) RecordSendFailure(devID, target, content string, at time.Time) error {
	imsi := r.resolveIMSI(devID, "")
	if imsi == "" {
		return nil
	}
	localPhone := r.localPhone(imsi)
	return db.SaveSMSWithLocalPhone(imsi, localPhone, localPhone, strings.TrimSpace(target), content, 2, 3, r.eventTime(at))
}

func (r vowifiSMSHistoryRecorder) RecordLocalNumberLearned(e eventhost.LocalNumberLearned) error {
	imsi := r.resolveIMSI(e.DevID, e.IMSI)
	number := strings.TrimSpace(e.Number)
	if number == "" {
		return nil
	}
	iccid := r.resolveICCID(e.DevID)
	if imsi == "" && iccid == "" {
		return nil
	}
	return db.RecordVoWiFiPhoneNumber(imsi, iccid, number)
}
