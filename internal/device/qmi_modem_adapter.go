package device

import (
	"context"
	"fmt"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost/identity"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

// qmiModemAdapter 将 QMIBackend 适配为 vowifi.Modem + simauth.ATModem 接口。
// 使 VoWiFi 在纯 QMI 模式下（无 AT 串口）也能完成 EAP-AKA SIM 鉴权。
//
// 核心原理：ATAKAProvider 的 APDU 构建/解析逻辑完全复用，
// 仅底层通道从 AT 串口切换为 QMI UIM 服务。
//
// 逻辑通道 APDU → QMI OpenLogicalChannel/SendAPDU/CloseLogicalChannel。
type qmiModemAdapter struct {
	deviceID string
	backend  backend.DeviceBackend
}

func newQMIModemAdapter(deviceID string, b backend.DeviceBackend) *qmiModemAdapter {
	return &qmiModemAdapter{
		deviceID: deviceID,
		backend:  b,
	}
}

// ============================================================================
// simauth.ATModem 接口实现
// ============================================================================

func (a *qmiModemAdapter) DeviceID() string { return a.deviceID }

// ExecuteATSilent 只保留接口占位；QMI VoWiFi 鉴权路径直接走逻辑通道。
func (a *qmiModemAdapter) ExecuteATSilent(cmd string, timeout time.Duration) (string, error) {
	return "", fmt.Errorf("QMI 模式不支持 AT 指令: %s", cmd)
}

func (a *qmiModemAdapter) OpenLogicalChannel(aid string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ch, err := a.backend.OpenLogicalChannel(ctx, aid)
	return ch, normalizeVoWiFiAPDUError(err)
}

func (a *qmiModemAdapter) ResolveLogicalChannelAID(app string, fallbackAID string) (string, string, error) {
	resolver, ok := a.backend.(backend.SIMAuthAIDResolver)
	if !ok {
		return fallbackAID, "fallback_backend_no_resolver", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return resolver.ResolveSIMAuthAID(ctx, app, fallbackAID)
}

func (a *qmiModemAdapter) CloseLogicalChannel(channel int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return normalizeVoWiFiAPDUError(a.backend.CloseLogicalChannel(ctx, channel))
}

func (a *qmiModemAdapter) TransmitAPDU(channel int, hexAPDU string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := a.backend.TransmitAPDU(ctx, channel, hexAPDU)
	return resp, normalizeVoWiFiAPDUError(err)
}

func (a *qmiModemAdapter) GetISIMIdentity() (identity.Identity, error) {
	return identity.ReadISIMIdentity(a)
}

// ============================================================================
// vowifi.Modem 接口实现（VoWiFi 生命周期管理）
// ============================================================================

func (a *qmiModemAdapter) IsHealthy() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	present, err := a.backend.IsSimInserted(ctx)
	if err != nil {
		return false
	}
	return present
}

func (a *qmiModemAdapter) IsSimInserted() bool {
	return a.IsHealthy()
}

func (a *qmiModemAdapter) QuerySIMInserted() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return a.backend.IsSimInserted(ctx)
}

func (a *qmiModemAdapter) GetRegStatus() (int, string) {
	// VoWiFi 工作在飞行模式下，注册态仅用于日志，不影响启动判定
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ss, err := a.backend.GetServingSystem(ctx)
	if err != nil || ss == nil {
		return 0, "unknown"
	}
	return ss.RegStatus, ss.RegStatusText
}

func (a *qmiModemAdapter) GetNetworkMode() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ss, err := a.backend.GetServingSystem(ctx)
	if err != nil || ss == nil {
		return ""
	}
	return ss.NetworkMode
}

func (a *qmiModemAdapter) Stop() {
	logger.Info("QMI modem adapter Stop() 被调用（不关闭 Backend，由 Worker 统一管理）",
		"device", a.deviceID)
}
