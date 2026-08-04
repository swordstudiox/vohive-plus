package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/identity"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

func (p *Pool) buildVoWiFiStartProfile(worker *Worker, traceID string) (identity.Profile, error) {
	if worker == nil {
		return identity.Profile{}, fmt.Errorf("worker_nil")
	}
	if worker.Backend == nil {
		return identity.Profile{}, fmt.Errorf("backend_not_available")
	}

	reader, ok := worker.Backend.(liveSIMIdentityReader)
	if !ok {
		return identity.Profile{}, fmt.Errorf("live_identity_not_supported")
	}

	liveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	imsi, err := reader.GetIMSILive(liveCtx)
	if err != nil {
		return identity.Profile{}, fmt.Errorf("实时读取 IMSI 失败: %w", err)
	}
	imsi = strings.TrimSpace(imsi)
	if imsi == "" {
		return identity.Profile{}, fmt.Errorf("实时 IMSI 为空")
	}

	status := worker.ProjectDeviceStatus()
	mcc, mnc, plmnSource := resolveVoWiFiProfileMCCMNC(liveCtx, worker, status, imsi, traceID)
	if mcc == "" || mnc == "" {
		return identity.Profile{}, fmt.Errorf("缺少 SIM 归属 MCC/MNC，无法构建 VoWiFi 启动画像: %s", imsi)
	}

	imei := strings.TrimSpace(status.IMEI)
	iccid := strings.TrimSpace(status.ICCID)

	smscCtx, smscCancel := context.WithTimeout(context.Background(), 5*time.Second)
	smsc, smscErr := worker.getSMSCWithContext(smscCtx)
	smscCancel()
	smsc = strings.TrimSpace(smsc)
	switch {
	case smsc != "":
		logger.Info("VoWiFi 启动前获取 SMSC 成功", "trace_id", traceID, "device", worker.ID, "smsc", smsc)
	case smscErr != nil:
		logger.Warn("VoWiFi 启动前获取 SMSC 失败，将以空 SMSC 继续启动", "trace_id", traceID, "device", worker.ID, "err", smscErr)
	default:
		logger.Warn("VoWiFi 启动前未获取到 SMSC，将以空 SMSC 继续启动", "trace_id", traceID, "device", worker.ID)
	}

	logger.Info("VoWiFi 启动画像将基于实时 IMSI 构建",
		"trace_id", traceID,
		"device", worker.ID,
		"source", "live_imsi",
		"plmn_source", plmnSource,
		"native_mcc", strings.TrimSpace(status.NativeMCC),
		"native_mnc", strings.TrimSpace(status.NativeMNC),
		"iccid", iccid,
		"imsi", imsi,
		"mcc", mcc,
		"mnc", mnc,
		"imei", imei)

	return buildVoWiFiRawProfile(imsi, mcc, mnc, imei, smsc), nil
}

func buildVoWiFiRawProfile(imsi, mcc, mnc, imei, smsc string) identity.Profile {
	return identity.Profile{
		IMSI: strings.TrimSpace(imsi),
		MCC:  strings.TrimSpace(mcc),
		MNC:  strings.TrimSpace(mnc),
		IMEI: strings.TrimSpace(imei),
		SMSC: strings.TrimSpace(smsc),
	}
}

func resolveVoWiFiProfileMCCMNC(ctx context.Context, worker *Worker, status modem.DeviceStatus, imsi, traceID string) (mcc, mnc, source string) {
	imsi = strings.TrimSpace(imsi)
	if worker != nil && worker.Backend != nil {
		if liveMCC, liveMNC, err := worker.Backend.GetNativeMCCMNC(ctx); err == nil {
			liveMCC = strings.TrimSpace(liveMCC)
			liveMNC = strings.TrimSpace(liveMNC)
			if liveMCC != "" && liveMNC != "" {
				cacheVoWiFiProfileMCCMNC(worker, liveMCC, liveMNC)
				return liveMCC, liveMNC, "sim_home"
			}
		} else {
			logger.Debug("VoWiFi 启动前读取 SIM 归属 MCC/MNC 失败，将回退已缓存 SIM 归属信息",
				"trace_id", traceID, "device", worker.ID, "err", err)
		}
	}

	statusIMSI := strings.TrimSpace(status.IMSI)
	if statusIMSI == imsi && strings.TrimSpace(status.NativeMCC) != "" && strings.TrimSpace(status.NativeMNC) != "" {
		return strings.TrimSpace(status.NativeMCC), strings.TrimSpace(status.NativeMNC), "sim_home_cache"
	}

	return vowifiProfileMCCMNC(status)
}

func cacheVoWiFiProfileMCCMNC(worker *Worker, mcc, mnc string) {
	if worker == nil || strings.TrimSpace(mcc) == "" || strings.TrimSpace(mnc) == "" {
		return
	}
	worker.cacheMu.Lock()
	worker.state.Identity.NativeMCC = strings.TrimSpace(mcc)
	worker.state.Identity.NativeMNC = strings.TrimSpace(mnc)
	worker.cacheMu.Unlock()
}

func vowifiProfileMCCMNC(status modem.DeviceStatus) (mcc, mnc, source string) {
	mcc = strings.TrimSpace(status.NativeMCC)
	mnc = strings.TrimSpace(status.NativeMNC)
	if mcc != "" && mnc != "" {
		return mcc, mnc, "sim_home_cache"
	}
	return "", "", ""
}

func newVoWiFiSIMReadyStartupState(deviceID, dataplaneMode, networkMode string, now time.Time) runtimehost.State {
	return runtimehost.State{
		Phase:         runtimehost.PhaseSIMReady,
		DeviceID:      deviceID,
		DataplaneMode: dataplaneMode,
		NetworkMode:   strings.TrimSpace(networkMode),
		SIMReady:      true,
		LastReason:    "sim_ready",
		UpdatedAt:     now,
	}
}

func (p *Pool) BuildVoWiFiStartProfile(worker *Worker) (identity.Profile, error) {
	return p.buildVoWiFiStartProfile(worker, "")
}
