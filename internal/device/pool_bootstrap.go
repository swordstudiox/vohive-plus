package device

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/esim"
	mbimcore "github.com/swordstudiox/vohive-plus/internal/mbim"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	qmicore "github.com/swordstudiox/vohive-plus/internal/qmi"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
	"github.com/swordstudiox/vohive-plus/pkg/smscodec"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
)

// deriveESIMTransport 从 device_backend 推导 eSIM 传输通道。
// device_backend=qmi 时 eSIM 走 QMI，device_backend=mbim 时走 MBIM，否则走 AT。
// 向下兼容：如果 esim_transport 有显式配置且 device_backend 为空，优先使用显式传输通道。
func deriveESIMTransport(cfg config.DeviceConfig) string {
	backend := strings.ToLower(strings.TrimSpace(cfg.DeviceBackend))
	legacy := strings.ToLower(strings.TrimSpace(cfg.ESIMTransport))

	switch backend {
	case "qmi":
		return config.ESIMTransportQMI
	case "mbim":
		return config.ESIMTransportMBIM
	case "at":
		return config.ESIMTransportAT
	}

	switch legacy {
	case config.ESIMTransportQMI, config.ESIMTransportMBIM:
		return legacy
	default:
		return config.ESIMTransportAT
	}
}

// resolveESIMTransport 在 deriveESIMTransport 基础上结合运行期能力做降级：
// MBIM 设备若模组不支持 MBIMEx UICC（esimTransportAvailable=false），降级回 AT，
// 与历史行为一致（避免在无可用 APDU 通道时阻断 worker 初始化）。
func resolveESIMTransport(cfg config.DeviceConfig, esimTransportAvailable bool) string {
	t := deriveESIMTransport(cfg)
	if t == config.ESIMTransportMBIM && !esimTransportAvailable {
		return config.ESIMTransportAT
	}
	return t
}

func resolvedBackendMode(cfg config.DeviceConfig) string {
	mode := strings.ToLower(strings.TrimSpace(cfg.DeviceBackend))
	if mode == "" && strings.TrimSpace(cfg.ControlDevice) != "" {
		return backend.BackendQMI
	}
	return backend.NormalizeBackendMode(mode)
}

func hasManagedQMINetwork(cfg config.DeviceConfig) bool {
	return strings.TrimSpace(cfg.ControlDevice) != "" && strings.TrimSpace(cfg.Interface) != ""
}

func requiresQMICore(cfg config.DeviceConfig) bool {
	if requiresMBIMCore(cfg) {
		return false
	}
	return hasManagedQMINetwork(cfg) ||
		resolvedBackendMode(cfg) != backend.BackendAT ||
		config.NormalizeESIMTransport(cfg.ESIMTransport) == config.ESIMTransportQMI
}

// needsATPortDiscovery 判断是否需要按 IMEI 反查 AT 端口:仅 AT 后端、且当前没有 AT
// 端口时才需要。MBIM 设备靠 control_device 起、压根没有 AT 口,绝不能进 AT 反查
// (否则会以"未找到匹配 IMEI 的 AT 端口"启动失败)。
func needsATPortDiscovery(cfg config.DeviceConfig) bool {
	return !requiresMBIMCore(cfg) && strings.TrimSpace(cfg.ATPort) == ""
}

func requiresMBIMCore(cfg config.DeviceConfig) bool {
	if resolvedBackendMode(cfg) == backend.BackendMBIM {
		return true
	}
	return strings.TrimSpace(cfg.DeviceBackend) == "" &&
		config.NormalizeESIMTransport(cfg.ESIMTransport) == config.ESIMTransportMBIM
}

func resolveDiscoveredQMIDevice(dev QMIDevice, timeout time.Duration, allowQMIIMEIProbe bool) (QMIDevice, string) {
	qmiClientOptions, allowQMIProbe := qmicore.DiscoveryClientOptionsForControlDevice(dev.ControlPath)
	return EnrichDiscoveredQMIDevice(dev, QMIDeviceEnrichOptions{
		EnableATProbe:      false,
		ATProbeTimeout:     timeout,
		EnableQMIIMEIProbe: allowQMIIMEIProbe && allowQMIProbe,
		QMIClientOptions:   qmiClientOptions,
	})
}

var resolveDiscoveredQMIDeviceFn = resolveDiscoveredQMIDevice

func resolveDiscoveredQMIDeviceWithConfig(dev QMIDevice, timeout time.Duration, allowQMIIMEIProbe bool, cfg config.DeviceConfig) (QMIDevice, string) {
	return EnrichDiscoveredQMIDevice(dev, QMIDeviceEnrichOptions{
		EnableATProbe:      false,
		ATProbeTimeout:     timeout,
		EnableQMIIMEIProbe: allowQMIIMEIProbe,
		QMIClientOptions:   qmicore.ClientOptionsFromDeviceConfig(cfg),
	})
}

var resolveDiscoveredCompatibleModemFn = resolveDiscoveredCompatibleModem

func resolveDiscoveredCompatibleModem(dev CompatibleModem, timeout time.Duration) (CompatibleModem, string) {
	return EnrichDiscoveredCompatibleModem(dev, CompatibleModemEnrichOptions{
		EnableATProbe:      true,
		ATProbeTimeout:     timeout,
		EnableQMIIMEIProbe: false,
	})
}

func configuredDevicesNeedCompatibleATDiscovery(devices []config.DeviceConfig) bool {
	for _, dev := range devices {
		if requiresQMICore(dev) {
			continue
		}
		if strings.TrimSpace(dev.ModemIMEI) == "" {
			continue
		}
		if strings.TrimSpace(dev.ATPort) != "" || strings.TrimSpace(dev.ManagePort) != "" {
			continue
		}
		return true
	}
	return false
}

type qmiBootstrapDiscoveryCache struct {
	loaded bool
	list   []QMIDevice
	err    error
}

func (c *qmiBootstrapDiscoveryCache) Get() ([]QMIDevice, error) {
	if c == nil {
		return discoverQMIDevicesFn()
	}
	if c.loaded {
		return c.list, c.err
	}
	c.loaded = true
	c.list, c.err = discoverQMIDevicesFn()
	return c.list, c.err
}

func buildESIMQMITransport(cfg config.DeviceConfig, qmiCore *qmicore.Manager) (esim.QMIAPDUTransport, esim.QMIAPDUTransportLifecycle, error) {
	if deriveESIMTransport(cfg) != config.ESIMTransportQMI {
		return nil, nil, nil
	}
	if strings.TrimSpace(cfg.ControlDevice) == "" {
		return nil, nil, fmt.Errorf("device_backend=%s 时必须提供 control_device", cfg.DeviceBackend)
	}
	if qmiCore != nil {
		return qmiCore, nil, nil
	}

	transport := esim.NewQMIUIMTransportWithOptions(cfg.ControlDevice, qmicore.ClientOptionsFromDeviceConfig(cfg))
	if err := transport.Start(); err != nil {
		return nil, nil, fmt.Errorf("启动独立 QMI UIM transport 失败: %w", err)
	}
	return transport, transport, nil
}

type mbimUICCProvider interface {
	ControlDevice() string
	OpenChannel(ctx context.Context, aid []byte) (uint32, error)
	CloseChannel(ctx context.Context, channel uint32) error
	TransmitAPDU(ctx context.Context, channel uint32, command []byte) ([]byte, error)
	ProbeUICCSupport(ctx context.Context) bool
}

func buildESIMMBIMTransport(mgr mbimUICCProvider) esim.QMIAPDUTransport {
	if mgr == nil || !mgr.ProbeUICCSupport(context.Background()) {
		logger.Info("[mbim] 模组不支持 MBIMEx UICC，eSIM 管理将降级回 AT")
		return nil
	}
	return esim.NewMBIMAPDUTransport(mgr)
}

type apduArbiterAwareTransport interface {
	SetAPDUArbiter(arbiter *apduarbiter.Arbiter)
}

func (p *Pool) AddWorkerFromConfig(devCfg config.DeviceConfig) (*Worker, error) {
	p.mu.Lock()
	if _, exists := p.workers[devCfg.ID]; exists {
		p.mu.Unlock()
		return nil, fmt.Errorf("设备已存在")
	}
	if p.rebuilding[devCfg.ID] {
		p.mu.Unlock()
		return nil, fmt.Errorf("设备 %s 正在初始化中，请勿重复触发", devCfg.ID)
	}
	if FreeDeviceLimitReached(len(p.workers)) {
		p.mu.Unlock()
		return nil, fmt.Errorf("%s", FreeDeviceWorkerLimitMessage())
	}
	p.rebuilding[devCfg.ID] = true
	attempt := p.beginRebuildAttemptLocked(devCfg.ID)
	p.mu.Unlock()

	// 启动看门狗：如果本次启动流程因内部某次探测卡死（如 vendored QMI 库未正确
	// 响应 context 取消）而长期不返回，强制释放 rebuilding 标记，避免设备槽位
	// 永久无法重试或删除。正常路径下下面的 defer 会在返回前 close(watchdogStop)
	// 让看门狗安静退出。
	watchdogStop := p.startBootstrapWatchdog(devCfg.ID, attempt, qmiWorkerBootstrapDeadline)
	defer func() {
		close(watchdogStop)
		p.endRebuildAttemptIfCurrent(devCfg.ID, attempt)
	}()

	needsQMICore := requiresQMICore(devCfg)
	if p.lifecycle != nil && needsQMICore {
		p.lifecycle.BeginRecovery(devCfg.ID, LifecyclePhaseWorkerStarting, "add_worker", qmiLifecycleRecoveryTTL)
	}

	// 前置检查：QMI 模式下优先信任可用的静态控制口；
	// 如果静态控制口缺失且配置了 IMEI，再进入发现流程尝试重绑新枚举路径。
	controlDeviceReady := false
	var controlDeviceStatErr error
	if ctrl := strings.TrimSpace(devCfg.ControlDevice); ctrl != "" {
		if _, err := os.Stat(ctrl); err != nil {
			controlDeviceStatErr = err
			canRebindByIMEI := requiresQMICore(devCfg) &&
				hasManagedQMINetwork(devCfg) &&
				strings.TrimSpace(devCfg.ModemIMEI) != ""
			if !canRebindByIMEI {
				return nil, fmt.Errorf("设备控制口 %s 不存在，可能模块尚未重新枚举: %w", ctrl, err)
			}
			logger.Debug("QMI 静态控制口不可用，将尝试按 IMEI 重新发现",
				"device", devCfg.ID,
				"control_device", ctrl,
				"err", err)
		} else {
			controlDeviceReady = true
		}
	}

	var matched *QMIDevice
	discoveryCache := &qmiBootstrapDiscoveryCache{}
	liveWorkerIndex := BuildWorkerDiscoveryIndex(p.GetAllWorkers(), false)
	configuredIndex := BuildConfiguredDeviceIndex(config.ListDevices())
	if !needsQMICore {
		liveWorkerIndex := BuildWorkerDiscoveryIndex(p.GetAllWorkers(), true)
		hardware := p.collectRescanHardware(nil, liveWorkerIndex)
		resolved := ResolveDeviceIdentities(hardware, []config.DeviceConfig{devCfg})

		if len(resolved.Matched) > 0 {
			hw := resolved.Matched[0].Hardware
			if devCfg.Interface == "" {
				devCfg.Interface = hw.NetInterface
			}
			if devCfg.ATPort == "" {
				devCfg.ATPort = hw.ATPort
				devCfg.ManagePort = hw.ATPort
			}
			if devCfg.AudioDevice == "" && hw.AudioDevice != "" {
				devCfg.AudioDevice = hw.AudioDevice
			}
			if devCfg.ControlDevice == "" {
				devCfg.ControlDevice = strings.TrimSpace(hw.ControlPath)
				devCfg.QMIDevice = strings.TrimSpace(hw.ControlPath)
			}
			if resolved.Matched[0].BackfillIMEI != "" {
				devCfg.ModemIMEI = resolved.Matched[0].BackfillIMEI
				logger.Info(fmt.Sprintf("[%s] MBIM/AT 通过 USB 路径认领成功，回填实时 IMEI", devCfg.ID), "imei", devCfg.ModemIMEI)
			}
		} else {
			if strings.TrimSpace(devCfg.ATPort) == "" && strings.TrimSpace(devCfg.ControlDevice) == "" {
				return nil, fmt.Errorf("未找到匹配的非 QMI 硬件（AT-only 或 MBIM），跳过启动")
			}
		}
	} else if hasManagedQMINetwork(devCfg) {
		configuredStatic := QMIDevice{
			ControlPath:  devCfg.ControlDevice,
			NetInterface: devCfg.Interface,
			ATPort:       devCfg.ATPort,
			USBPath:      devCfg.USBPath,
		}
		selected := configuredStatic
		selectedByDiscovery := false
		if !controlDeviceReady && strings.TrimSpace(devCfg.ModemIMEI) != "" {
			qmiList, qmiErr := discoveryCache.Get()
			if qmiErr != nil {
				logger.Debug("QMI 静态路径启动前发现失败", "device", devCfg.ID, "err", qmiErr)
			} else {
				hardware := p.collectRescanHardware(qmiList, liveWorkerIndex)
				resolved := ResolveDeviceIdentities(hardware, []config.DeviceConfig{devCfg})
				if len(resolved.Matched) > 0 {
					hw := resolved.Matched[0].Hardware
					selected = QMIDevice{
						ControlPath:  strings.TrimSpace(hw.ControlPath),
						NetInterface: hw.NetInterface,
						ATPort:       hw.ATPort,
						USBPath:      hw.USBPath,
					}
					selectedByDiscovery = true
				}
			}
		}
		if !controlDeviceReady && !selectedByDiscovery && controlDeviceStatErr != nil {
			return nil, fmt.Errorf("设备控制口 %s 不存在，可能模块尚未重新枚举: %w",
				strings.TrimSpace(devCfg.ControlDevice), controlDeviceStatErr)
		}
		matched = &selected
	} else if imei := strings.TrimSpace(devCfg.ModemIMEI); imei != "" {
		if list, err := discoveryCache.Get(); err == nil {
			for i := range list {
				_, claimedByLive := liveWorkerIndex.Lookup(list[i].ControlPath, list[i].USBPath, list[i].NetInterface)
				configuredID := configuredIndex.Lookup(list[i].ControlPath, list[i].USBPath, list[i].NetInterface, "")
				if claimedByLive || (configuredID != "" && configuredID != devCfg.ID) {
					continue
				}
				allowQMIIMEIProbe := configuredID == ""
				d, got := resolveDiscoveredQMIDeviceWithConfig(list[i], 1600*time.Millisecond, allowQMIIMEIProbe, devCfg)
				if got == "" || !config.IMEIMatches(got, imei) {
					continue
				}
				matched = &d
				devCfg.Interface = d.NetInterface
				devCfg.ControlDevice = d.ControlPath
				devCfg.QMIDevice = d.ControlPath
				if devCfg.USBPath == "" {
					devCfg.USBPath = d.USBPath
				}
				devCfg.ATPort = d.ATPort
				devCfg.ManagePort = d.ATPort
				break
			}
		}
		if matched == nil {
			return nil, fmt.Errorf("未找到匹配 IMEI 的设备: %s", imei)
		}
	}
	if matched != nil {
		devCfg = applyQMIManagedAttachment(devCfg, *matched)
	}

	m, err := modem.New(devCfg)
	if err != nil {
		return nil, fmt.Errorf("初始化 Modem 失败: %w", err)
	}

	backendMode := resolvedBackendMode(devCfg)
	isQMIRequired := requiresQMICore(devCfg)
	isMBIMRequired := requiresMBIMCore(devCfg)

	var qmiCore *qmicore.Manager
	if isQMIRequired {
		if matched == nil {
			if list, err := discoveryCache.Get(); err == nil {
				for i := range list {
					d, _ := resolveDiscoveredQMIDeviceWithConfig(list[i], 1600*time.Millisecond, false, devCfg)
					list[i] = d
				}
				if found, ok := BuildStaticQMIDeviceIndex(list).Lookup(devCfg.ControlDevice, devCfg.USBPath, devCfg.Interface); ok {
					matched = &found
				}
			}
		}
		var managerDevice *qmimanager.ModemDevice
		if matched != nil {
			md := matched.ToQMIManagerDevice()
			managerDevice = &md
		}
		qmiCore = qmicore.New(devCfg, managerDevice)
	}
	var mbimCore *mbimcore.Manager
	var mbimSource backend.MBIMSource
	if isMBIMRequired {
		mbimCore = mbimcore.New(devCfg.ControlDevice, config.NormalizeMBIMTransport(devCfg.MBIMTransport))
		mbimCore.SetDataConfig(mbimcore.DataConfig{APN: devCfg.APN, Interface: devCfg.Interface, IPVersion: devCfg.IPVersion})
		if err := mbimCore.Open(p.ctx); err != nil {
			m.Stop()
			return nil, fmt.Errorf("MBIM 控制通道打开失败: %w", err)
		}
		mbimSource = mbimCore
	}
	qmiTransport, qmiTransportLifecycle, err := buildESIMQMITransport(devCfg, qmiCore)
	if err != nil {
		if mbimCore != nil {
			_ = mbimCore.Close()
		}
		m.Stop()
		if qmiCore != nil {
			qmiCore.Stop()
		}
		return nil, err
	}
	if qmiTransport == nil && mbimCore != nil {
		qmiTransport = buildESIMMBIMTransport(mbimCore)
	}

	w := &Worker{
		ID:               devCfg.ID,
		Config:           devCfg,
		Modem:            m,
		QMICore:          qmiCore,
		MBIMCore:         mbimCore,
		ESIMQMITransport: qmiTransportLifecycle,
		APDUArbiter:      apduarbiter.New(devCfg.ID, apduarbiter.Options{MaxLeaseHold: 10 * time.Minute, MaxSessions: 3, MaxQMITransports: 3}),
		Pool:             p,
		stop:             make(chan struct{}),
		reassembler:      smscodec.NewReassembler(),
	}
	p.assignWorkerGeneration(w)
	configureWorkerAPDUArbiter(w, qmiTransport)

	be, err := newWorkerBackendStrict(devCfg.ID, backendMode, devCfg.ControlDevice, m, qmiCore, mbimSource)
	if err != nil {
		if qmiTransportLifecycle != nil {
			_ = qmiTransportLifecycle.Stop()
		}
		if mbimCore != nil {
			_ = mbimCore.Close()
		}
		if qmiCore != nil {
			qmiCore.Stop()
		}
		m.Stop()
		return nil, err
	}
	w.Backend = be
	onBeforeSwitch, onAfterSwitch, onSwitchFailed, onSwitchDegraded, onSwitchPhase := p.newESIMSwitchCallbacks(devCfg.ID)
	w.EsimMgr, err = newESIMManagerForWorker(w, qmiTransport, onBeforeSwitch, onAfterSwitch, onSwitchFailed, onSwitchDegraded, onSwitchPhase)
	if err != nil {
		if qmiTransportLifecycle != nil {
			_ = qmiTransportLifecycle.Stop()
		}
		if mbimCore != nil {
			_ = mbimCore.Close()
		}
		if qmiCore != nil {
			qmiCore.Stop()
		}
		m.Stop()
		return nil, fmt.Errorf("初始化 eSIM 管理器失败: %w", err)
	}
	p.bindESIMUIMIndications(w)
	p.bindQMIStateIndications(w)
	p.bindQMIHealthIndications(w)
	if mbimCore != nil {
		p.bindMBIMStateIndications(w)
		p.bindMBIMSlotIndications(w)
		p.bindMBIMHealthIndications(w)
	}

	if qmiCore != nil {
		var resetRecoveryRunning atomic.Bool
		qmiCore.OnModemReset(func() {
			if p.lifecycle != nil {
				p.lifecycle.BeginRecovery(w.ID, LifecyclePhaseRecovering, "qmi_modem_reset", qmiLifecycleRecoveryTTL)
			}
			w.markHealthRecoveryWindow(qmiHealthGraceAfterReset)
			logger.Warn("QMI 检测到模组重置，启用健康检查恢复窗口",
				"device", w.ID,
				"window", qmiHealthGraceAfterReset.String())
			if w.EsimMgr != nil {
				w.EsimMgr.NotifyModemResetDelayed(qmiHealthGraceAfterReset)
			}
			// 模组重置后 QMI Core 恢复需要 30-60 秒，恢复后数据面可能未自动连接。
			// 使用 CAS 确保同一时间只有一个恢复 goroutine 在运行，防止合并事件导致重复启动。
			if !resetRecoveryRunning.CompareAndSwap(false, true) {
				return
			}
			go func() {
				defer resetRecoveryRunning.Store(false)
				for attempt := 1; attempt <= 6; attempt++ {
					select {
					case <-p.ctx.Done():
						return
					case <-w.stop:
						return
					case <-time.After(time.Duration(10*attempt) * time.Second):
					}
					// 延迟等待后再次检查 worker 是否已被移除
					select {
					case <-w.stop:
						return
					default:
					}
					if current := p.GetWorker(w.ID); current != w {
						logger.Debug("模组重置恢复：Worker 已失效被移除，中止恢复", "device", w.ID)
						return
					}
					nc := w.NetworkController()
					if nc == nil {
						return
					}
					if nc.IsConnected() {
						logger.Debug("模组重置恢复：数据面已连接，跳过重建", "device", w.ID)
						return
					}
					if err := p.applyNetworkPreference(w); err != nil {
						logger.Debug("模组重置恢复：应用网络偏好失败，稍后重试",
							"device", w.ID, "attempt", attempt, "err", err)
						continue
					}
					logger.Info("模组重置恢复：数据面已重建", "device", w.ID, "attempt", attempt)
					p.refreshIPs(w, true)
					return
				}
			}()
		})

		qmiCore.SetOnConnect(func() {
			p.markQMIControlRecovered(w, "qmi_connected")
			p.refreshIPs(w, true)
			p.notifyDataConnected(w.ID)
		})
	}

	if backendUsesATRuntime(backendMode) {
		m.SetOnDisconnectWithReason(func(reason string) {
			devID := w.ID
			if strings.TrimSpace(reason) == "" {
				reason = "modem_disconnect"
			}
			logger.Warn(fmt.Sprintf("[%s] 检测到模块掉线，将进入重启恢复扫描", devID), "reason", reason)
			p.scheduleATDisconnectRecovery(devID, reason)
		})
		p.bindModemReadyIndications(w)
	}

	if backendMode == backend.BackendQMI {
		w.smsMode = smsModeQMI
		if smsCore := w.smsQMICore(); smsCore != nil {
			smsCore.OnNewSMSWithStorage(func(storage uint8, index uint32) {
				logger.Info(fmt.Sprintf("[%s] 收到 QMI 短信 URC 通知", w.ID), "index", index, "storage", storage)
				w.handleNewSMSQMI(storage, index)
			})
			smsCore.OnNewSMSRaw(func(info qmicore.RawSMSIndication) {
				logger.Info(fmt.Sprintf("[%s] 收到 QMI 原始短信通知", w.ID),
					"pdu_len", len(info.PDU),
					"ack_required", info.AckRequired,
					"transaction_id", info.TransactionID,
					"format", info.Format,
				)
				w.handleNewSMSRawQMI(info)
			})
		}
		// 纯 QMI 模式不监听 AT URC；AT 口仅保留给人工 AT 终端。
	} else if backendMode == backend.BackendMBIM {
		w.smsMode = smsModeMBIM
		if mbimCore != nil {
			mbimCore.OnNewSMS(func() {
				logger.Info(fmt.Sprintf("[%s] 收到 MBIM 短信通知", w.ID))
				w.handleNewSMSMBIM("indication")
			})
		}
		// 纯 MBIM 模式不监听 AT URC；短信通过 MBIM SMS service 接收。
	} else {
		w.smsMode = smsModeAT
		m.SetNewSMSHandler(nil)
		m.SetDisableURCRead(false)
		m.SetSMSCallback(func(sender, content string, timestamp time.Time) {
			w.processSMS(sender, content, timestamp)
		})
	}
	logger.Info(fmt.Sprintf("[%s] 短信模式已配置", w.ID), "sms_mode", w.smsMode.String(), "backend", backendMode)

	if err := m.Start(); err != nil {
		if qmiTransportLifecycle != nil {
			_ = qmiTransportLifecycle.Stop()
		}
		if mbimCore != nil {
			_ = mbimCore.Close()
		}
		if qmiCore != nil {
			qmiCore.Stop()
		}
		m.Stop()
		return nil, fmt.Errorf("启动 Modem 管理器失败: %w", err)
	}

	if !m.WaitReady(5 * time.Second) {
		logger.Warn(fmt.Sprintf("[%s] Modem 初始化超时，继续启动 QMI Core", devCfg.ID))
	}
	if qmiCore == nil {
		cleanupWorkerStartupSIMAuthLogicalChannels(w)
	}

	if !p.isRebuildAttemptCurrent(devCfg.ID, attempt) {
		// 看门狗已判定本次启动超时并释放了 rebuilding 占位，期间可能已有更新的
		// 尝试在进行；放弃用这条过期路径注册 worker，避免覆盖最新状态。
		if qmiTransportLifecycle != nil {
			_ = qmiTransportLifecycle.Stop()
		}
		if qmiCore != nil {
			qmiCore.Stop()
		}
		m.Stop()
		return nil, fmt.Errorf("设备 %s 启动流程已超时放弃，可能已被新一轮尝试接管", devCfg.ID)
	}

	qmiWorkerRegistered := false
	if qmiCore != nil {
		if err := p.registerWorkerStarting(w); err != nil {
			if qmiTransportLifecycle != nil {
				_ = qmiTransportLifecycle.Stop()
			}
			qmiCore.Stop()
			m.Stop()
			return nil, err
		}
		qmiWorkerRegistered = true
		if err := p.startQMICoreWithStartupBudget(w, "qmi_start_core"); err != nil {
			logger.Warn(fmt.Sprintf("[%s] 启动 QMI Core 失败", devCfg.ID), "err", err)
			if qmiTransportLifecycle != nil {
				_ = qmiTransportLifecycle.Stop()
			}
			if mbimCore != nil {
				_ = mbimCore.Close()
			}
			p.removeWorkerRegistrationIfCurrent(w)
			if qmiCore != nil {
				qmiCore.Stop()
			}
			m.Stop()
			return nil, fmt.Errorf("启动 QMI Core 失败: %w", err)
		}
	}

	if !qmiWorkerRegistered {
		p.mu.Lock()
		p.workers[devCfg.ID] = w
		p.mu.Unlock()
	}
	w.uimIndicationsReady.Store(true)
	p.scheduleATRadioWarmup(w, "startup")

	go func(worker *Worker) {
		select {
		case <-p.ctx.Done():
			return
		case <-worker.stop:
			return
		default:
		}
		worker.PreWarmCache()
		// 缓存预热完成后，触发一次状态广播，强制前端 SSE 推流更新最新设备数据
		p.broadcastVoWiFiStateChange(worker.ID)
	}(w)

	go func(worker *Worker) {
		// QMI Core 初始化可能需要 15-30 秒（重启后甚至更久）。
		// 使用递增延迟重试，确保数据面在 QMI Core 就绪后被建立。
		retryDelays := []time.Duration{3 * time.Second, 5 * time.Second, 7 * time.Second, 10 * time.Second, 15 * time.Second}

		for i, delay := range retryDelays {
			select {
			case <-p.ctx.Done():
				return
			case <-worker.stop:
				return
			case <-time.After(delay):
			}

			_, err := p.refreshIdentityAndApplyCardPolicy(worker, "startup_post_apply")
			if err == nil && worker.CurrentICCID() != "" {
				return
			}

			if i < len(retryDelays)-1 {
				logger.Debug(fmt.Sprintf("[%s] 启动期卡策略应用尚未完成，稍后重试", worker.ID),
					"attempt", i+1, "err", err)
			}
		}
		logger.Warn(fmt.Sprintf("[%s] 启动期卡策略应用最终未完成", worker.ID))
	}(w)

	go func(worker *Worker) {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-p.ctx.Done():
				return
			case <-worker.stop:
				return
			case <-ticker.C:
				switch worker.smsMode {
				case smsModeAT:
				case smsModeQMI:
					if worker.QMICore != nil {
						if err := worker.CheckAllSMSQMI(); err != nil {
							logger.Warn(fmt.Sprintf("[%s] QMI 轮询短信失败", worker.ID), "err", err)
						}
					}
				case smsModeMBIM:
					worker.handleNewSMSMBIM("poll")
				case smsModeVoWiFi:
				}
			}
		}
	}(w)

	// 老配置(无 IMEI)首次起来后,尽力学到 live IMEI 并回填,完成身份锚定;
	// 失败只记日志,不影响已成功启动的设备。
	if strings.TrimSpace(devCfg.ModemIMEI) == "" && w.Backend != nil {
		imeiCtx, cancel := context.WithTimeout(p.ctx, 3*time.Second)
		if live, err := w.Backend.GetIMEI(imeiCtx); err == nil && strings.TrimSpace(live) != "" {
			devCfg.ModemIMEI = strings.TrimSpace(live)
			logger.Info("老配置已学到 live IMEI,回填身份", "device", devCfg.ID, "imei", devCfg.ModemIMEI)
		}
		cancel()
	}

	p.persistDeviceAttachmentsIfChanged(devCfg)

	return w, nil
}
