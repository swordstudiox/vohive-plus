package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

func (p *Pool) stopVoWiFiAppForTeardown(ctx context.Context, deviceID, reason string) bool {
	return p.voWiFiHost().StopInstanceForTeardown(ctx, deviceID, reason)
}

func (p *Pool) teardownVoWiFiForReconnect(deviceID string) bool {
	return p.voWiFiHost().TeardownForReconnect(p.ctx, deviceID)
}

func (p *Pool) RestoreSMSMode(deviceID string) {
	p.restoreSMSModeAfterVoWiFiTeardown(p.GetWorker(deviceID))
}

func (p *Pool) restoreSMSModeAfterVoWiFiTeardown(w *Worker) {
	if w == nil {
		return
	}

	if w.Backend != nil && w.Backend.Mode() != "at" {
		w.smsMode = smsModeQMI
		if w.Modem != nil {
			w.Modem.SetDisableURCRead(true)
		}
	} else {
		w.smsMode = smsModeAT
		if w.Modem != nil {
			w.Modem.SetDisableURCRead(false)
			w.Modem.ExecuteATSilent("AT+CNMI=2,1,0,0,0", 2*time.Second)
			w.Modem.SetSMSCallback(func(sender, content string, timestamp time.Time) {
				w.processSMS(sender, content, timestamp)
			})
		}
	}

	logger.Info("短信模式已恢复", "device", w.ID, "sms_mode", w.smsMode.String())
	w.restoreNetworkAfterVoWiFi = false
}

func (p *Pool) RestoreRadioAfterVoWiFi(deviceID string) error {
	p.mu.RLock()
	isRebuilding := p.rebuilding[deviceID]
	p.mu.RUnlock()
	if isRebuilding {
		return fmt.Errorf("设备 %s 仍处于重建流程中", deviceID)
	}
	if p.IsESIMSwitching(deviceID) {
		return fmt.Errorf("设备 %s 正在切卡，暂不允许恢复射频", deviceID)
	}

	w := p.GetWorker(deviceID)
	if w == nil {
		return fmt.Errorf("设备 %s 不存在", deviceID)
	}
	if w.Backend == nil {
		return nil
	}

	logger.Info("退出飞行模式恢复射频", "device", deviceID, "backend", w.Backend.Mode())
	if err := w.Backend.SetOperatingMode(p.ctx, backend.ModeOnline); err != nil {
		return err
	}

	logger.Info("等待射频及基带完全启动重获端口控制权...", "device", deviceID)
	if err := p.waitRadioRecoveryReady(deviceID, 5*time.Second); err != nil {
		logger.Warn("等待射频恢复关键路径就绪超时", "device", deviceID, "err", err)
	}
	waitCtx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	if err := waitForCondition(waitCtx, 200*time.Millisecond, func() bool {
		return p.GetWorker(deviceID) != nil
	}); err != nil {
		logger.Warn("等待设备恢复健康超时，继续后续流程", "device", deviceID, "err", err)
	}
	return nil
}

func (p *Pool) RequestVoWiFiRecover(deviceID, reason string) error {
	return p.voWiFiHost().Recover(p.ctx, vowifihost.LifecycleRecoverRequest{DeviceID: deviceID, Reason: reason})
}

func (p *Pool) DisableVoWiFi(deviceID ...string) error {
	if len(deviceID) > 0 {
		target := strings.TrimSpace(deviceID[0])
		if target != "" {
			if p.IsESIMSwitching(target) {
				return fmt.Errorf("设备 %s 正在切卡，暂不允许停用 VoWiFi", target)
			}
			if err := p.voWiFiHost().Disable(p.ctx, target, "disable", false); err != nil {
				return err
			}
			p.applyCardPolicyAfterVoWiFiDisable(target, "vowifi_disabled")
			return nil
		}
	}

	devIDs := p.voWiFiHost().InstanceIDs()

	for _, devID := range devIDs {
		if err := p.voWiFiHost().Disable(p.ctx, devID, "disable_all", false); err != nil {
			return err
		}
		p.applyCardPolicyAfterVoWiFiDisable(devID, "vowifi_disabled")
	}
	return nil
}

func (p *Pool) applyCardPolicyAfterVoWiFiDisable(deviceID, reason string) {
	if p == nil {
		return
	}
	w := p.GetWorker(deviceID)
	if w == nil {
		return
	}
	p.resolveAndApplyPolicy(w, reason)
}

func (p *Pool) RestartVoWiFi(deviceID string) error {
	return p.voWiFiHost().Restart(p.ctx, deviceID)
}
