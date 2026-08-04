package device

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/iniwex5/vowifi-go/engine/swu"
	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/carrier"
	"github.com/iniwex5/vowifi-go/runtimehost/identity"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/db"
	innersim "github.com/swordstudiox/vohive-plus/internal/sim"
	"github.com/swordstudiox/vohive-plus/internal/upstreamproxy"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

type voWiFiStartContext struct {
	worker *Worker
	modem  runtimehost.Modem
	vowifihost.PreparedStart
	startedAt time.Time
}

type workerAKAProviderInput struct {
	worker   *Worker
	deviceID string
	modem    runtimehost.Modem
}

func (w workerAKAProviderInput) BackendMode() string {
	if w.worker == nil || w.worker.Backend == nil {
		return ""
	}
	return w.worker.Backend.Mode()
}

func (w workerAKAProviderInput) MBIMAKAProvider() (innersim.BackendAKAProvider, bool) {
	if w.worker == nil || w.worker.Backend == nil {
		return nil, false
	}
	provider, ok := w.worker.Backend.(interface {
		CalculateAKA(ctx context.Context, rand16, autn16 []byte) (res, ik, ck, auts []byte, err error)
	})
	if !ok || !strings.EqualFold(w.worker.Backend.Mode(), backend.BackendMBIM) {
		return nil, false
	}
	return provider, true
}

func (w workerAKAProviderInput) MBIMCapability() (*mbim.Capabilities, bool) {
	if w.worker == nil || w.worker.Backend == nil {
		return nil, false
	}
	cp, ok := w.worker.Backend.(interface{ Capability() *mbim.Capabilities })
	if !ok {
		return nil, false
	}
	c := cp.Capability()
	return c, c != nil
}

func (w workerAKAProviderInput) RuntimeModem() (innersim.ATModem, error) {
	modemIface := w.modem
	if modemIface == nil {
		var err error
		modemIface, err = BuildVoWiFiRuntimeModem(w.worker, w.deviceID)
		if err != nil {
			return nil, err
		}
	}
	modem, ok := modemIface.(innersim.ATModem)
	if !ok {
		return nil, fmt.Errorf("device %s runtime modem does not implement sim.ATModem", strings.TrimSpace(w.deviceID))
	}
	return modem, nil
}

func BuildAKAProvider(w *Worker, deviceID string) innersim.AKAProvider {
	return innersim.BuildAKAProvider(workerAKAProviderInput{
		worker:   w,
		deviceID: deviceID,
	})
}

func (p *Pool) Context() context.Context {
	if p == nil || p.ctx == nil {
		return context.Background()
	}
	return p.ctx
}

func (p *Pool) PrepareStart(deviceID, traceID, runtimeEPDGOverride string) (vowifihost.PreparedStart, error) {
	startCtx, err := p.prepareVoWiFiStartContext(deviceID, traceID, runtimeEPDGOverride)
	if err != nil {
		return vowifihost.PreparedStart{}, err
	}
	prepared := startCtx.PreparedStart
	prepared.Modem = startCtx.modem
	return prepared, nil
}

func (p *Pool) BeforeStart(deviceID string, modemIface runtimehost.Modem, proxyCfg *runtimehost.ProxyConfig) func(context.Context, runtimehost.SessionConfig) error {
	return p.beforeVoWiFiStart(deviceID, modemIface, proxyCfg)
}

func (p *Pool) HandleStartupError(req vowifihost.StartupErrorRequest) error {
	return p.handleVoWiFiStartupError(req.TraceID, req.DeviceID, req.RuntimeEPDGOverride, req.Generation, req.StartedAt, p.GetWorker(req.DeviceID), req.State, req.Err)
}

func (p *Pool) MarkRuntimeStarted(req vowifihost.RuntimeStartedRequest) {
	w := p.GetWorker(req.DeviceID)
	if w == nil {
		return
	}
	w.smsMode = smsModeVoWiFi
	if w.Modem != nil {
		w.Modem.SetNewSMSHandler(nil)
		w.Modem.SetSMSCallback(nil)
		w.Modem.SetDisableURCRead(true)
	}
}

func (p *Pool) prepareVoWiFiStartContext(deviceID, traceID, runtimeEPDGOverride string) (voWiFiStartContext, error) {
	startCtx := voWiFiStartContext{startedAt: time.Now()}

	w := p.GetWorker(deviceID)
	if w == nil {
		return startCtx, fmt.Errorf("设备 %s 不存在", deviceID)
	}
	startCtx.worker = w

	modemIface, errModemIface := newVoWiFiModemInterface(w, deviceID)
	if errModemIface != nil {
		return startCtx, errModemIface
	}
	startCtx.modem = modemIface
	if _, ok := modemIface.(*qmiModemAdapter); ok {
		logger.Info("VoWiFi 使用 QMI 模式鉴权", "trace_id", traceID, "device", deviceID)
	}

	w.cacheMu.RLock()
	identityReady := w.state.Identity.Ready
	w.cacheMu.RUnlock()
	if !identityReady {
		if err := w.RefreshIdentityLive(nil, "enable_vowifi"); err != nil {
			logger.Error("VoWiFi 启动前刷新当前设备身份失败",
				"trace_id", traceID,
				"device", deviceID,
				"err", err)
			return startCtx, err
		}
		p.PersistIdentityState(w)
	}

	currentStatus := w.ProjectDeviceStatus()
	logger.Info("VoWiFi 启动前读取当前设备身份",
		"trace_id", traceID,
		"device", deviceID,
		"iccid", strings.TrimSpace(currentStatus.ICCID),
		"imsi", strings.TrimSpace(currentStatus.IMSI),
		"imei", strings.TrimSpace(currentStatus.IMEI))

	startProfile, errProfile := p.buildVoWiFiStartProfile(w, traceID)
	if errProfile != nil {
		logger.Error("构建 VoWiFi 启动画像失败", "trace_id", traceID, "device", deviceID, "err", errProfile)
		return startCtx, errProfile
	}
	startCtx.Profile = startProfile

	akaProvider := innersim.BuildAKAProvider(workerAKAProviderInput{
		worker:   w,
		deviceID: deviceID,
		modem:    modemIface,
	})
	if akaProvider == nil {
		if strings.EqualFold(workerAKAProviderInput{worker: w}.BackendMode(), backend.BackendMBIM) {
			return startCtx, fmt.Errorf("设备 %s 的 MBIM 不支持 AKA(AUTH 与逻辑通道均不可用),如需 VoWiFi 请切 QMI 组态", deviceID)
		}
		return startCtx, fmt.Errorf("设备 %s 无可用 AKA provider", deviceID)
	}
	if strings.EqualFold(workerAKAProviderInput{worker: w}.BackendMode(), backend.BackendMBIM) {
		logger.Info("VoWiFi 使用 MBIM Auth(AKA) 鉴权", "trace_id", traceID, "device", deviceID)
	} else {
		logger.Info("VoWiFi 使用 APDU(AKA) 鉴权", "trace_id", traceID, "device", deviceID)
	}
	startCtx.SIM = runtimehost.NewReaderSIMAdapter(akaProvider)

	if carrier.IsVoWiFiBlockedMCC(startProfile.MCC) {
		err := carrier.NewVoWiFiBlockedMCCError(startProfile.MCC)
		logger.Warn("VoWiFi 启动被运营商策略拦截",
			"trace_id", traceID,
			"device", deviceID,
			"mcc", formatVoWiFiPLMN3(startProfile.MCC),
			"imsi", startProfile.IMSI,
			"err", err)
		logVoWiFiFailureSummary(traceID, deviceID, "startup", "policy", err.Error(), false, 0)
		return startCtx, err
	}

	runtimehost.SetLogger(logger.ZapLogger())
	prepared, errPrepare := identity.PrepareStart(identity.PrepareStartInput{
		DeviceID:            deviceID,
		Profile:             startProfile,
		RuntimeEPDGOverride: runtimeEPDGOverride,
		Access:              runtimehost.NewModemAccessAdapter(modemIface),
	})
	if errPrepare != nil {
		logger.Warn("VoWiFi 启动画像准备失败",
			"trace_id", traceID,
			"device", deviceID,
			"err", errPrepare)
		logVoWiFiFailureSummary(traceID, deviceID, "startup", "identity", errPrepare.Error(), false, 0)
		return startCtx, errPrepare
	}
	startCtx.Prepared = prepared
	logger.Info("VoWiFi 启动画像已准备",
		"trace_id", traceID,
		"device", deviceID,
		"matched_plmn", prepared.EffectiveCarrier.MCC+"/"+prepared.EffectiveCarrier.MNC,
		"preset_id", prepared.EffectiveCarrier.PresetID,
		"epdg_source", prepared.EPDGSource,
		"epdg", prepared.EPDGAddr,
		"identity_source", prepared.IdentityIMEISource,
		"requested_source", prepared.IMSIdentity.RequestedSource,
		"actual_source", prepared.IMSIdentity.ActualSource,
		"aka_app_preference", prepared.IMSIdentity.AKAAppPreference,
		"applied", prepared.IMSIdentity.Applied)

	if nc := w.NetworkController(); nc != nil {
		w.restoreNetworkAfterVoWiFi = w.Config.NetworkEnabled
		logger.Info("VoWiFi 启用中，停止网络功能", "trace_id", traceID, "device", deviceID)
		if err := nc.Disconnect(); err != nil {
			logger.Warn("断开数据连接失败，继续启动 VoWiFi", "trace_id", traceID, "device", deviceID, "err", err)
		}
		w.clearCachedIP()
	}

	// 切卡恢复场景下设备可能已处于飞行模式，此时无需再次切换。
	// 冗余的 SetOperatingMode(LowPower) 会触发模组内部 UIM Session Close，
	// 导致 SIM 卡基础通道上的 USIM 应用选择状态丢失，使后续 AKA 认证失败（SW=6B00）。
	alreadyInFlight := false
	if opMode, opErr := w.Backend.GetOperatingMode(p.ctx); opErr == nil {
		alreadyInFlight = isFlightOperatingMode(opMode)
	}
	if strings.EqualFold(w.Backend.Mode(), backend.BackendMBIM) {
		logger.Info("MBIM 后端不支持真正的低功耗模式",
			"trace_id", traceID, "device", deviceID)
	} else if alreadyInFlight {
		logger.Info("设备已处于飞行模式，跳过冗余的飞行模式切换",
			"trace_id", traceID, "device", deviceID, "backend", w.Backend.Mode())
	} else {
		logger.Info("进入飞行模式以禁用原生 IMS 注册",
			"trace_id", traceID, "device", deviceID, "backend", w.Backend.Mode())
		if err := w.Backend.SetOperatingMode(p.ctx, backend.ModeRFOff); err != nil {
			logger.Warn("进入飞行模式失败，继续尝试建立隧道",
				"trace_id", traceID, "device", deviceID, "err", err)
		} else {
			time.Sleep(500 * time.Millisecond)
		}
	}

	startCtx.Proxy = resolveVoWiFiCountryProxy(startProfile.MCC, traceID, deviceID)

	startCtx.NetworkMode = modemIface.GetNetworkMode()
	startCtx.StartupState = newVoWiFiSIMReadyStartupState(deviceID, swu.DataplaneModeUserspace, startCtx.NetworkMode, time.Now())
	p.recordVoWiFiStartupState(deviceID, startCtx.StartupState)
	return startCtx, nil
}

func resolveVoWiFiCountryProxy(homeMCC, traceID, deviceID string) *runtimehost.ProxyConfig {
	proxy, countryCode, err := db.GetHomeMCCUpstreamProxy(homeMCC)
	if err != nil {
		logger.Warn("VoWiFi 启动前读取国家前置代理配置失败",
			"trace_id", traceID,
			"device", deviceID,
			"home_mcc", strings.TrimSpace(homeMCC),
			"err", err)
		return nil
	}
	if proxy == nil {
		logger.Info("VoWiFi 国家前置代理未命中，使用直连",
			"trace_id", traceID,
			"device", deviceID,
			"home_mcc", strings.TrimSpace(homeMCC),
			"proxy_country_code", countryCode,
			"mcc_table_ready", upstreamproxy.CountryTableReady(),
			"proxy_route", "direct")
		return nil
	}
	logger.Info("VoWiFi 国家前置代理已命中",
		"trace_id", traceID,
		"device", deviceID,
		"home_mcc", strings.TrimSpace(homeMCC),
		"proxy_country_code", countryCode,
		"upstream_proxy_id", proxy.ID,
		"proxy_route", "country_rule")
	return &runtimehost.ProxyConfig{
		ID:       proxy.ID,
		Addr:     proxy.Addr,
		Username: proxy.Username,
		Password: proxy.Password,
		Enabled:  proxy.Enabled,
	}
}

func (p *Pool) beforeVoWiFiStart(deviceID string, modemIface runtimehost.Modem, proxyCfg *runtimehost.ProxyConfig) func(context.Context, runtimehost.SessionConfig) error {
	return func(startCtx context.Context, cfg runtimehost.SessionConfig) error {
		startupState := newVoWiFiSIMReadyStartupState(deviceID, cfg.DataplaneMode, modemIface.GetNetworkMode(), time.Now())
		startupState.RegStatus, startupState.RegStatusText = modemIface.GetRegStatus()
		p.recordVoWiFiStartupState(deviceID, startupState)
		if proxyCfg != nil && proxyCfg.Enabled && strings.TrimSpace(proxyCfg.Addr) != "" {
			probeRes, probeErr := upstreamproxy.ProbeSOCKS5(startCtx, upstreamproxy.ProbeConfig{
				ProxyAddr: proxyCfg.Addr,
				Username:  proxyCfg.Username,
				Password:  proxyCfg.Password,
				Timeout:   5 * time.Second,
			})
			if probeErr != nil {
				startupState.LastErrorClass = "proxy"
				startupState.LastError = probeErr.Error()
				startupState.LastReason = probeRes.FailureSummary()
				p.recordVoWiFiStartupState(deviceID, startupState)
				return fmt.Errorf("前置代理自检失败: %w", probeErr)
			}
		}
		return nil
	}
}
