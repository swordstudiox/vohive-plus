package device

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/identity"
	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func newVoWiFiModemInterface(w *Worker, deviceID string) (runtimehost.Modem, error) {
	if w == nil {
		return nil, fmt.Errorf("设备 %s 不存在", deviceID)
	}
	if strings.TrimSpace(deviceID) == "" {
		deviceID = strings.TrimSpace(w.ID)
	}
	if w.Backend != nil {
		mode := strings.ToLower(strings.TrimSpace(w.Backend.Mode()))
		if mode != "" && mode != backend.BackendAT {
			return newQMIModemAdapter(deviceID, w.Backend), nil
		}
	}
	if w.Modem != nil {
		return newModemAdapter(w.Modem), nil
	}
	return nil, fmt.Errorf("设备 %s 的 Modem/Backend 均未初始化，无法启动 VoWiFi", deviceID)
}

func BuildVoWiFiRuntimeModem(w *Worker, deviceID string) (runtimehost.Modem, error) {
	return newVoWiFiModemInterface(w, deviceID)
}

type modemAdapter struct {
	m *modem.Manager
}

func newModemAdapter(m *modem.Manager) *modemAdapter {
	return &modemAdapter{m: m}
}

func (a *modemAdapter) DeviceID() string                { return a.m.DeviceID() }
func (a *modemAdapter) IsHealthy() bool                 { return a.m.IsHealthy() }
func (a *modemAdapter) IsSimInserted() bool             { return a.m.IsSimInserted() }
func (a *modemAdapter) QuerySIMInserted() (bool, error) { return a.m.QuerySIMInserted() }
func (a *modemAdapter) GetRegStatus() (int, string)     { return a.m.GetRegStatus() }
func (a *modemAdapter) ExecuteATSilent(cmd string, timeout time.Duration) (string, error) {
	return a.m.ExecuteATSilent(cmd, timeout)
}
func (a *modemAdapter) OpenLogicalChannel(aid string) (int, error) {
	ch, err := a.m.OpenSIMAuthLogicalChannel(aid)
	return ch, normalizeVoWiFiAPDUError(err)
}
func (a *modemAdapter) ResolveLogicalChannelAID(app string, fallbackAID string) (string, string, error) {
	return a.m.ResolveSIMAuthAID(app, fallbackAID)
}
func (a *modemAdapter) CloseLogicalChannel(channel int) error {
	return normalizeVoWiFiAPDUError(a.m.CloseSIMAuthLogicalChannel(channel))
}
func (a *modemAdapter) TransmitAPDU(channel int, hexAPDU string) (string, error) {
	resp, err := a.m.TransmitAPDU(channel, hexAPDU)
	return resp, normalizeVoWiFiAPDUError(err)
}
func (a *modemAdapter) GetISIMIdentity() (identity.Identity, error) {
	return identity.ReadISIMIdentity(a)
}
func (a *modemAdapter) GetNetworkMode() string {
	mode := a.m.GetFullStatus().NetworkMode
	if mode == "" {
		if v, err := a.m.QueryNetworkModeFallback(); err == nil {
			mode = v
		}
	}
	return mode
}
func (a *modemAdapter) Stop() { a.m.Stop() }

func normalizeVoWiFiAPDUError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, apduarbiter.ErrAPDUBusy) {
		return fmt.Errorf("%w: %v", runtimehost.ErrAPDUBusy, err)
	}
	return err
}
