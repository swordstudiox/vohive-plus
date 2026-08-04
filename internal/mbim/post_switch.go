package mbimcore

import (
	"context"
	"fmt"
	"strings"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func isMBIMTransportFatal(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if msg == "" {
		return false
	}
	for _, fragment := range []string{
		"read failed: eof",
		"broken pipe",
		"no such device",
		"no such file or directory",
		"failed to open",
		"device not opened",
	} {
		if strings.Contains(msg, fragment) {
			return true
		}
	}
	return false
}

func mapMBIMReadyState(readyState uint32) (uimReady bool, cardPresent bool, simStatus qmi.SIMStatus, reason qmimanager.UIMReadinessReason) {
	switch readyState {
	case 1:
		return true, true, qmi.SIMReady, qmimanager.UIMReadinessReady
	case 2:
		return false, false, qmi.SIMAbsent, qmimanager.UIMReadinessCardAbsent
	case 6:
		return false, true, qmi.SIMBlocked, qmimanager.UIMReadinessSIMBlocked
	default:
		return false, true, qmi.SIMNotReady, qmimanager.UIMReadinessCardResetting
	}
}

func (m *Manager) GetUIMReadiness(ctx context.Context) (qmimanager.UIMReadiness, error) {
	sub, err := m.SubscriberReady(ctx)
	if err != nil {
		if isMBIMTransportFatal(err) {
			return qmimanager.UIMReadiness{
				TransportReady: false,
				ControlReady:   false,
				Reason:         qmimanager.UIMReadinessTransportFatal,
				Err:            err,
			}, err
		}
		return qmimanager.UIMReadiness{
			TransportReady: true,
			ControlReady:   false,
			Reason:         qmimanager.UIMReadinessControlUnavailable,
			Err:            err,
		}, err
	}

	uimReady, cardPresent, simStatus, reason := mapMBIMReadyState(sub.ReadyState)
	out := qmimanager.UIMReadiness{
		TransportReady: true,
		ControlReady:   true,
		UIMReady:       uimReady,
		CardPresent:    cardPresent,
		SIMStatus:      simStatus,
		ICCID:          strings.TrimSpace(sub.ICCID),
		IMSI:           strings.TrimSpace(sub.IMSI),
		Reason:         reason,
	}
	if out.Reason == qmimanager.UIMReadinessReady && out.ICCID == "" && out.IMSI == "" {
		out.Reason = qmimanager.UIMReadinessIdentityEmpty
	}

	if d, derr := m.device(); derr == nil && m.qmiReadUsable() {
		if frame, ferr := d.QMIUIMGetCardStatus(ctx); ferr == nil {
			slot, known, source, perr := mbim.ParseActiveSlot(frame)
			if perr == nil {
				out.ActiveSlot = slot
				out.SlotKnown = known
				out.SlotSource = source
			}
		}
	}
	return out, nil
}

func (m *Manager) setRadioState(ctx context.Context, sw mbim.RadioSwitch) (mbim.RadioState, error) {
	if hook := m.setRadioStateHook; hook != nil {
		return hook(ctx, sw)
	}
	return m.SetRadioState(ctx, sw)
}

func (m *Manager) UIMPowerOffSIM(ctx context.Context, slot uint8) error {
	if d, err := m.device(); err == nil && m.qmiReadUsable() {
		return d.UIMPowerOffSIM(ctx, slot)
	}
	_, err := m.setRadioState(ctx, mbim.RadioOff)
	return err
}

func (m *Manager) UIMPowerOnSIM(ctx context.Context, slot uint8) error {
	if d, err := m.device(); err == nil && m.qmiReadUsable() {
		firstErr := d.UIMPowerOnSIM(ctx, slot)
		_, radioErr := m.setRadioState(ctx, mbim.RadioOn)
		if firstErr != nil && radioErr != nil {
			return fmt.Errorf("qmi power on failed: %v; radio on failed: %w", firstErr, radioErr)
		}
		if radioErr != nil {
			return radioErr
		}
		return firstErr
	}
	_, err := m.setRadioState(ctx, mbim.RadioOn)
	return err
}
