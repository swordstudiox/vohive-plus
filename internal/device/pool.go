package device

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/voicehost"
	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/cardpolicy"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/esim"
	mbimcore "github.com/swordstudiox/vohive-plus/internal/mbim"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	"github.com/swordstudiox/vohive-plus/internal/proxy/server"
	qmicore "github.com/swordstudiox/vohive-plus/internal/qmi"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
	"github.com/swordstudiox/vohive-plus/pkg/smscodec"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
)

// smsMode 短信工作模式枚举
type smsMode int

const (
	smsModeAT     smsMode = iota // AT URC 驱动（+CMTI → AT+CMGR）
	smsModeQMI                   // QMI WMS 驱动（EventNewSMS + 定时轮询）
	smsModeVoWiFi                // IMS 驱动，AT/QMI 短信全禁
	smsModeMBIM                  // MBIM SMS service 驱动（SMS_READ indication + 定时轮询）
)

const qmiSMSStorageUnknown uint8 = 0xFF

type qmiSMSCore interface {
	OnNewSMSWithStorage(func(storage uint8, index uint32))
	OnNewSMSRaw(func(qmicore.RawSMSIndication))
	ListSMS(storageType uint8, tag qmi.MessageTagType) ([]struct {
		Index uint32
		Tag   qmi.MessageTagType
	}, error)
	ReadSMS(preferredStorage uint8, index uint32) (*qmimanager.DecodedSMS, error)
	WMSDeleteMessage(ctx context.Context, storageType uint8, index uint32) error
	AckRawSMS(ctx context.Context, info qmicore.RawSMSIndication, success bool) error
}

type liveSIMIdentityReader interface {
	GetICCIDLive(ctx context.Context) (string, error)
	GetIMSILive(ctx context.Context) (string, error)
}

type liveSIMSPNReader interface {
	GetNativeSPNLive(ctx context.Context) (string, error)
}

type liveSIMMetadataReader interface {
	GetSIMMetadataLive(ctx context.Context) (*backend.SIMMetadata, error)
}

const publicIPLookupWait = 6 * time.Second

const (
	defaultESIMPostSwitchMinDelay = time.Second
	qmiHealthFailureThreshold     = 3
	qmiHealthGraceAfterSwitch     = 2 * time.Minute
	qmiHealthGraceAfterReset      = 90 * time.Second
)

func (m smsMode) String() string {
	switch m {
	case smsModeAT:
		return "AT"
	case smsModeQMI:
		return "QMI"
	case smsModeVoWiFi:
		return "VoWiFi"
	case smsModeMBIM:
		return "MBIM"
	default:
		return "unknown"
	}
}

type Worker struct {
	ID          string
	Config      config.DeviceConfig
	generation  uint64
	Modem       *modem.Manager
	Backend     backend.DeviceBackend // 双模后端接口（AT / QMI / Auto）
	QMICore     *qmicore.Manager
	MBIMCore    *mbimcore.Manager
	netOverride NetworkController
	APDUArbiter *apduarbiter.Arbiter
	qmiSMS      qmiSMSCore
	// ESIMQMITransport 仅在未创建共享 QMI Core、但 eSIM 仍需走 QMI transport 时使用。
	// 复用 QMICore/QMI Core 场景下为 nil。
	ESIMQMITransport esim.QMIAPDUTransportLifecycle
	Proxy            *server.Server
	Pool             *Pool
	EsimMgr          *esim.Manager
	stop             chan struct{}
	stopOnce         sync.Once

	cachedIP            string
	cachedPublicIPv6    string
	cacheTime           time.Time
	cacheMu             sync.RWMutex
	state               deviceStateStore
	rotateMu            sync.Mutex // 防止并发换 IP
	consecutiveFailures int        // 连续切换失败次数

	publicIPRetryMu    sync.Mutex
	publicIPRetryCount int
	publicIPRetryTimer *time.Timer

	ipRefreshMu       sync.Mutex
	ipRefreshInFlight bool
	ipRefreshLast     time.Time

	qmiRegistrationMu       sync.Mutex
	qmiRegistrationInFlight bool

	operatorScanMu      sync.Mutex
	operatorScanCurrent OperatorScanResult
	operatorScanCancel  context.CancelFunc
	operatorScanActive  bool

	reassembler *smscodec.Reassembler

	// 短信工作模式：AT / QMI / VoWiFi（三模完全隔离）
	smsMode smsMode

	// 记录离开 VoWiFi 后是否应按配置恢复数据网络。
	restoreNetworkAfterVoWiFi bool

	healthMu                  sync.Mutex
	healthConsecutiveFailures int
	healthGraceUntil          time.Time
	healthSnapshot            HealthSnapshot

	streamSubs          atomic.Int32 // 单设备的流订阅计数器
	uimIndicationsReady atomic.Bool  // worker 完成启动注册后才处理 UIM 事件触发的重扫/重载
	// switchEvents receives UIM indications for the active eSIM switch; nil outside switch convergence.
	switchEvents atomic.Pointer[switchEventSource]
}

type Pool struct {
	workers    map[string]*Worker
	rebuilding map[string]bool // 标记设备是否正在重载
	// rebuildAttempt 记录每个设备最近一次 AddWorkerFromConfig 尝试的递增 token。
	// 用于让启动看门狗超时强制释放 rebuilding 后，滞后完成的旧启动流程能识别自己
	// 已被新一轮尝试取代，从而放弃注册而不是用过期路径覆盖最新状态。
	rebuildAttempt map[string]uint64
	// workerGenerations records the latest accepted worker generation per device.
	// It prevents stale callbacks from an old Worker from driving recovery for a newer Worker.
	workerGenerations map[string]uint64
	transportRecovery *TransportRecoveryController
	// modemRebootRecovering 只用于模组重启恢复去重；不能复用 rebuilding，
	// 否则恢复扫描内的 AddWorkerFromConfig 会被自己的标记挡住。
	modemRebootRecovering     map[string]bool
	modemRebootWakeups        map[string]chan struct{}
	cfg                       *config.Config
	notifier                  Notifier
	mu                        sync.RWMutex
	ctx                       context.Context
	cancel                    context.CancelFunc
	dataConnectHandlersMu     sync.RWMutex
	dataConnectHandlers       []func(deviceID string)
	rescanAndReconnectForTest func() error

	voiceGateway *voicehost.Gateway

	// VoWiFi host 侧整合（多实例）
	vowifiHost         *vowifihost.Manager
	lifecycle          *lifecycleCoordinator
	simEventMu         sync.Mutex
	simEventTimers     map[string]*time.Timer
	deviceEventWakeMu  sync.Mutex
	deviceEventWakeups map[string]*deviceEventRecoverWakeup

	switchMu         sync.Mutex
	switchingDevices map[string]bool
	switchContexts   map[string]esimSwitchContext
	switchTokens     map[string]uint64
	switchSeq        uint64

	// 概览监控页面流定阅数统计
	overviewSubs atomic.Int32

	// 热插拔监听
	udevWatcher    *UdevWatcher
	startOnce      sync.Once
	policyResolver cardpolicy.Resolver
}

func NewPool(cfg *config.Config) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		workers:               make(map[string]*Worker),
		rebuilding:            make(map[string]bool),
		rebuildAttempt:        make(map[string]uint64),
		workerGenerations:     make(map[string]uint64),
		modemRebootRecovering: make(map[string]bool),
		modemRebootWakeups:    make(map[string]chan struct{}),
		cfg:                   cfg,
		ctx:                   ctx,
		cancel:                cancel,
		vowifiHost:            vowifihost.NewManager(),
		simEventTimers:        make(map[string]*time.Timer),
		deviceEventWakeups:    make(map[string]*deviceEventRecoverWakeup),
		switchingDevices:      make(map[string]bool),
		switchContexts:        make(map[string]esimSwitchContext),
		switchTokens:          make(map[string]uint64),
		lifecycle:             newLifecycleCoordinator(),
	}
	p.transportRecovery = NewTransportRecoveryController(p)
	p.voWiFiHost().ConfigureAdapter(p)
	p.voWiFiHost().ConfigureRuntimeDependencies(p.GetVoiceGateway(), vowifiDeliveryStore{}, poolVoWiFiRuntimeDispatcher{pool: p})

	return p
}

func (p *Pool) OnDataConnected(handler func(deviceID string)) {
	if p == nil || handler == nil {
		return
	}
	p.dataConnectHandlersMu.Lock()
	p.dataConnectHandlers = append(p.dataConnectHandlers, handler)
	p.dataConnectHandlersMu.Unlock()
}

func (p *Pool) notifyDataConnected(deviceID string) {
	if p == nil {
		return
	}
	p.dataConnectHandlersMu.RLock()
	handlers := append([]func(string){}, p.dataConnectHandlers...)
	p.dataConnectHandlersMu.RUnlock()
	for _, handler := range handlers {
		h := handler
		go h(deviceID)
	}
}

func (p *Pool) nextWorkerGenerationLocked(deviceID string) uint64 {
	if p.workerGenerations == nil {
		p.workerGenerations = make(map[string]uint64)
	}
	next := p.workerGenerations[deviceID] + 1
	p.workerGenerations[deviceID] = next
	return next
}

func (p *Pool) assignWorkerGeneration(worker *Worker) uint64 {
	if p == nil || worker == nil {
		return 0
	}
	p.mu.Lock()
	generation := p.nextWorkerGenerationLocked(worker.ID)
	p.mu.Unlock()
	worker.generation = generation
	if p.transportRecovery != nil {
		p.transportRecovery.SetWorkerGeneration(worker.ID, generation)
	}
	return generation
}

func (p *Pool) registerWorkerStarting(worker *Worker) error {
	if p == nil || worker == nil || strings.TrimSpace(worker.ID) == "" {
		return fmt.Errorf("worker_nil")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if current := p.workers[worker.ID]; current != nil && current != worker {
		return fmt.Errorf("设备已存在")
	}
	p.workers[worker.ID] = worker
	if worker.generation == 0 {
		worker.generation = p.nextWorkerGenerationLocked(worker.ID)
	}
	return nil
}

func (p *Pool) removeWorkerRegistrationIfCurrent(worker *Worker) {
	if p == nil || worker == nil || strings.TrimSpace(worker.ID) == "" {
		return
	}
	p.mu.Lock()
	if current := p.workers[worker.ID]; current == worker {
		delete(p.workers, worker.ID)
	}
	p.mu.Unlock()
}

func (w *Worker) IncStreamSub() {
	w.streamSubs.Add(1)
}

func (w *Worker) DecStreamSub() {
	w.streamSubs.Add(-1)
}

func (w *Worker) StreamSubCount() int32 {
	return w.streamSubs.Load()
}

func normalizeNotifier(n Notifier) Notifier {
	if n == nil {
		return nil
	}
	v := reflect.ValueOf(n)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		if v.IsNil() {
			return nil
		}
	}
	return n
}

func (p *Pool) getNotifier() Notifier {
	p.mu.RLock()
	n := p.notifier
	p.mu.RUnlock()
	return normalizeNotifier(n)
}

func (p *Pool) SetNotifier(n Notifier) {
	n = normalizeNotifier(n)

	p.mu.Lock()
	p.notifier = n
	p.mu.Unlock()

	instances := p.voWiFiHost().Instances()
	apps := make([]*runtimehost.Instance, 0, len(instances))
	for _, app := range instances {
		apps = append(apps, app)
	}

	for _, app := range apps {
		if n == nil {
			app.SetNotifier(nil)
			app.SetSMSNotifier(nil)
			continue
		}
		notifier := n
		app.SetNotifier(func(msg string) {
			notifier.NotifyRaw(msg)
		})
		app.SetSMSNotifier(func(deviceID, sender, content string, ts time.Time) {
			if withSource, ok := notifier.(SMSSourceNotifier); ok {
				withSource.NotifySMSWithSource(deviceID, sender, content, "VoWiFi", ts)
				return
			}
			notifier.NotifySMS(deviceID, sender, content, ts)
		})
	}
}

// IsESIMSwitching reports whether the specified device is in an eSIM switch flow.
func (p *Pool) IsESIMSwitching(deviceID string) bool {
	p.switchMu.Lock()
	defer p.switchMu.Unlock()
	return p.switchingDevices[deviceID]
}

func atRadioReadOptionsForReason(reason string) ATRadioReadOptions {
	switch strings.TrimSpace(reason) {
	case "overview_detail", "startup_post_apply", "startup_warm_runtime", "startup_radio_warmup", "manual_refresh":
		return ATRadioReadOptions{Attempts: 3, Delay: 500 * time.Millisecond}
	default:
		return ATRadioReadOptions{Attempts: 1}
	}
}

func (w *Worker) collectRuntimeStatus(ctx context.Context, reason string) modem.DeviceStatus {
	if w.Backend != nil && w.Backend.Mode() != "at" {
		if ctx == nil {
			ctx = context.Background()
		}

		status := modem.DeviceStatus{}
		var mu sync.Mutex
		var wg sync.WaitGroup

		call := func(f func()) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f()
			}()
		}

		call(func() {
			if v, err := w.Backend.GetIMEI(ctx); err == nil {
				mu.Lock()
				status.IMEI = v
				mu.Unlock()
			}
		})
		call(func() {
			if v, err := w.Backend.GetRevision(ctx); err == nil {
				mu.Lock()
				status.Firmware = v
				mu.Unlock()
			}
		})
		call(func() {
			if sig, err := w.Backend.GetSignalInfo(ctx); err == nil && sig != nil {
				mu.Lock()
				status.SignalDBM = sig.RSSI
				status.SignalRSRP = sig.RSRP
				status.SignalRSRQ = sig.RSRQ
				status.SignalSINR = sig.SINR
				status.NR5GSignalSINR = sig.NR5GSINR
				mu.Unlock()
			}
		})
		call(func() {
			if ss, err := w.Backend.GetServingSystem(ctx); err == nil && ss != nil {
				mu.Lock()
				status.Operator = ss.Operator
				status.NetworkMode = ss.NetworkMode
				status.NetworkDuplex = ss.NetworkDuplex
				status.RadioBand = ss.RadioBand
				status.RadioChannel = ss.RadioChannel
				status.RegStatus = ss.RegStatus
				status.RegStatusText = ss.RegStatusText
				status.PSAttached = ss.PSAttached
				status.LAC = ss.LAC
				status.CellID = ss.CellID
				mu.Unlock()
			}
		})
		call(func() {
			if inserted, err := w.Backend.IsSimInserted(ctx); err == nil {
				mu.Lock()
				status.SimInserted = inserted
				mu.Unlock()
			}
		})
		call(func() {
			if opMode, err := w.Backend.GetOperatingMode(ctx); err == nil {
				m := int(opMode)
				mu.Lock()
				status.OperatingMode = &m
				mu.Unlock()
			}
		})

		wg.Wait()
		return status
	}

	status := modem.DeviceStatus{}
	if w.Modem != nil {
		status = w.Modem.GetFullStatus()
	}
	if w.Modem != nil {
		snapshot := ReadATRadioSnapshot(ctx, w.Modem, atRadioReadOptionsForReason(reason))
		status = snapshot.ApplyToStatus(status)
	}
	if w.Backend != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		if opMode, err := w.Backend.GetOperatingMode(ctx); err == nil {
			m := int(opMode)
			status.OperatingMode = &m
		}
	}
	status.ICCID = ""
	status.IMSI = ""
	return status
}

func (w *Worker) RefreshRuntime(ctx context.Context, reason string) error {
	if w == nil {
		return fmt.Errorf("worker_nil")
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
	}

	status := w.collectRuntimeStatus(ctx, reason)
	healthy := w.IsDeviceHealthy()

	w.cacheMu.Lock()
	updated := w.mergeRuntimeStateLocked(status, healthy)
	if !updated {
		w.state.Meta.Healthy = healthy
	}
	w.cacheMu.Unlock()
	return nil
}

type liveSIMIdentityRefreshResult struct {
	ICCID string
	IMSI  string
}

func (w *Worker) RefreshIdentityLive(ctx context.Context, reason string) error {
	_, err := w.refreshIdentityLive(ctx, reason)
	return err
}

func (w *Worker) refreshIdentityLive(ctx context.Context, reason string) (liveSIMIdentityRefreshResult, error) {
	if w == nil {
		return liveSIMIdentityRefreshResult{}, fmt.Errorf("worker_nil")
	}
	if w.Backend == nil {
		return liveSIMIdentityRefreshResult{}, fmt.Errorf("backend_not_available")
	}
	reader, ok := w.Backend.(liveSIMIdentityReader)
	if !ok {
		return liveSIMIdentityRefreshResult{}, fmt.Errorf("live_identity_not_supported")
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
	}

	iccid, imsi, nativeSPN := "", "", ""
	if v, err := reader.GetICCIDLive(ctx); err == nil {
		iccid = strings.TrimSpace(v)
		if iccid == "" {
			logger.Debug("读取 ICCID 为空", "device", w.ID, "reason", reason)
		}
	} else {
		logger.Debug("读取 ICCID 失败", "device", w.ID, "reason", reason, "err", err)
	}
	if v, err := reader.GetIMSILive(ctx); err == nil {
		imsi = strings.TrimSpace(v)
		if imsi == "" {
			logger.Debug("读取 IMSI 为空", "device", w.ID, "reason", reason)
		}
	} else {
		logger.Debug("读取 IMSI 失败", "device", w.ID, "reason", reason, "err", err)
	}
	spnReadOK := false
	if spnReader, ok := w.Backend.(liveSIMSPNReader); ok {
		if v, err := spnReader.GetNativeSPNLive(ctx); err == nil {
			nativeSPN = strings.TrimSpace(v)
			spnReadOK = true
		} else {
			logger.Debug("读取 EF_SPN 失败", "device", w.ID, "reason", reason, "err", err)
		}
	}
	var simMetadata *backend.SIMMetadata
	metadataReadOK := false
	if metadataReader, ok := w.Backend.(liveSIMMetadataReader); ok {
		if meta, err := metadataReader.GetSIMMetadataLive(ctx); err == nil {
			simMetadata = meta
			metadataReadOK = true
		}
	}
	if simMetadata == nil ||
		strings.TrimSpace(simMetadata.NativeMCC) == "" ||
		strings.TrimSpace(simMetadata.NativeMNC) == "" {
		if mcc, mnc, err := w.Backend.GetNativeMCCMNC(ctx); err == nil {
			mcc = strings.TrimSpace(mcc)
			mnc = strings.TrimSpace(mnc)
			if mcc != "" && mnc != "" {
				if simMetadata == nil {
					simMetadata = &backend.SIMMetadata{}
				}
				simMetadata.NativeMCC = mcc
				simMetadata.NativeMNC = mnc
				metadataReadOK = true
			}
		} else {
			logger.Debug("读取 SIM 归属 MCC/MNC 失败", "device", w.ID, "reason", reason, "err", err)
		}
	}
	result := liveSIMIdentityRefreshResult{ICCID: iccid, IMSI: imsi}
	if iccid == "" && imsi == "" && nativeSPN == "" && !hasSIMMetadata(simMetadata) {
		return result, fmt.Errorf("live_identity_empty")
	}

	now := time.Now()
	w.cacheMu.Lock()
	phase := w.state.Identity.Phase
	targetICCID := normalizeSIMIdentityForCompare(w.state.Identity.TargetICCID)
	if targetICCID != "" && (phase == simIdentityPhaseTransitioning || phase == simIdentityPhaseDegraded) &&
		normalizeSIMIdentityForCompare(iccid) != targetICCID {
		err := fmt.Errorf("live_identity_target_not_active")
		w.state.Identity.LastReason = strings.TrimSpace(reason)
		w.state.Identity.LastError = err.Error()
		w.state.Meta.IdentityUpdatedAt = now
		w.state.Meta.UpdatedAt = now
		w.cacheMu.Unlock()
		return result, err
	}
	identityChangedForSPN := (iccid != "" && iccid != strings.TrimSpace(w.state.Identity.ICCID)) ||
		(imsi != "" && imsi != strings.TrimSpace(w.state.Identity.IMSI))
	if iccid != "" {
		w.state.Identity.ICCID = iccid
	}
	if imsi != "" {
		w.state.Identity.IMSI = imsi
	}
	if nativeSPN != "" {
		w.state.Identity.NativeSPN = nativeSPN
	} else if identityChangedForSPN && spnReadOK {
		w.state.Identity.NativeSPN = ""
	}
	if w.mergeSIMMetadataLocked(simMetadata) || metadataReadOK {
		if identityChangedForSPN && !hasSIMMetadata(simMetadata) {
			w.clearSIMMetadataLocked()
		}
	} else if identityChangedForSPN {
		w.clearSIMMetadataLocked()
	}
	w.state.Identity.Ready = true
	w.state.Identity.Phase = simIdentityPhaseReady
	w.state.Identity.TargetICCID = ""
	w.state.Identity.LastReason = strings.TrimSpace(reason)
	w.state.Identity.LastError = ""
	w.state.Meta.IdentityUpdatedAt = now
	w.state.Meta.UpdatedAt = now
	w.cacheMu.Unlock()
	return result, nil
}

// bindQMIStateIndications 订阅 SIM 卡状态变化事件。
// 切卡场景的身份刷新已收敛到 post_switch_finalize 单路径，这里不再触发补刷新。
func (p *Pool) bindQMIStateIndications(worker *Worker) {
	if worker == nil || worker.QMICore == nil {
		return
	}

	worker.QMICore.OnSimStatusChanged(func() {
		logger.Info("[事件驱动] SIM 状态变化", "device", worker.ID)
		p.handleSIMStatusEvent(worker.ID, "qmi_sim_status", nil, "")
		p.wakeDesiredVoWiFiRecoverFromDeviceEvent(worker.ID, "post_switch_qmi_sim_status")
		go func() {
			if err := p.applyNetworkPreference(worker); err != nil {
				logger.Warn("SIM 状态变化后 QMI 网络偏好协调失败", "device", worker.ID, "err", err)
			}
		}()
	})
}

func (p *Pool) bindMBIMStateIndications(worker *Worker) {
	if worker == nil || worker.MBIMCore == nil {
		return
	}

	worker.MBIMCore.OnSimStatusChanged(func() {
		logger.Info("[事件驱动] MBIM SIM 状态变化", "device", worker.ID)
		p.handleSIMStatusEvent(worker.ID, "mbim_sim_status", nil, "")
		p.wakeDesiredVoWiFiRecoverFromDeviceEvent(worker.ID, "post_switch_mbim_sim_status")
	})
}

func (p *Pool) bindMBIMSlotIndications(worker *Worker) {
	if worker == nil || worker.MBIMCore == nil {
		return
	}
	worker.MBIMCore.OnSlotStatus(func(slotIndex, state uint32) {
		logger.Debug("收到 MBIM 卡槽状态指示", "device", worker.ID, "slot", slotIndex, "state", state)
		p.wakeDesiredVoWiFiRecoverFromDeviceEvent(worker.ID, "mbim_slot_status")
	})
}

func (p *Pool) bindMBIMHealthIndications(worker *Worker) {
	if worker == nil || worker.MBIMCore == nil {
		return
	}
	worker.MBIMCore.OnRecoveryExhausted(func(reason string, err error) {
		p.maybeScheduleTransportRebuild(worker, HealthLayerMBIM, reason, err)
	})
	worker.MBIMCore.OnHealth(func(event mbimcore.HealthEvent) {
		switch event.State {
		case mbimcore.HealthEventHealthy:
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:     HealthLayerMBIM,
				State:     HealthStateHealthy,
				EventType: string(event.State),
				Reason:    event.Reason,
				At:        event.At,
			})
			worker.resetHealthFailureStreak()
		case mbimcore.HealthEventSuspect:
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:     HealthLayerMBIM,
				State:     HealthStateSuspect,
				EventType: string(event.State),
				Reason:    event.Reason,
				At:        event.At,
			})
		case mbimcore.HealthEventRecovering:
			recoveryUntil := time.Now().Add(qmiHealthGraceAfterReset)
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:         HealthLayerMBIM,
				State:         HealthStateRecovering,
				EventType:     string(event.State),
				Reason:        event.Reason,
				RecoveryUntil: recoveryUntil,
				At:            event.At,
			})
			if p.lifecycle != nil {
				p.lifecycle.BeginRecovery(worker.ID, LifecyclePhaseRecovering, event.Reason, qmiLifecycleRecoveryTTL)
			}
		}
	})
}

func (p *Pool) bindQMIHealthIndications(worker *Worker) {
	if worker == nil || worker.QMICore == nil {
		return
	}
	worker.QMICore.OnRecoveryExhausted(func(reason string, err error) {
		p.maybeScheduleTransportRebuild(worker, HealthLayerQMI, reason, err)
	})
	worker.QMICore.OnHealthEvent(func(event qmicore.HealthEvent) {
		switch event.State {
		case qmicore.HealthEventHealthy:
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:     HealthLayerQMI,
				State:     HealthStateHealthy,
				EventType: string(event.State),
				Reason:    event.Reason,
				At:        event.At,
			})
			worker.resetHealthFailureStreak()
		case qmicore.HealthEventSuspect:
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:     HealthLayerQMI,
				State:     HealthStateSuspect,
				EventType: string(event.State),
				Reason:    event.Reason,
				At:        event.At,
			})
		case qmicore.HealthEventRecovering:
			recoveryUntil := time.Now().Add(qmiHealthGraceAfterReset)
			worker.RecordWatchdogEvent(WatchdogEvent{
				Layer:         HealthLayerQMI,
				State:         HealthStateRecovering,
				EventType:     string(event.State),
				Reason:        event.Reason,
				RecoveryUntil: recoveryUntil,
				At:            event.At,
			})
			if p != nil && p.lifecycle != nil {
				p.lifecycle.BeginRecovery(worker.ID, LifecyclePhaseRecovering, event.Reason, qmiLifecycleRecoveryTTL)
			}
		}
	})
}

func (p *Pool) handleSIMStatusEvent(deviceID, source string, insertedHint *bool, state string) {
	deviceID = strings.TrimSpace(deviceID)
	if p == nil || deviceID == "" {
		return
	}
	if insertedHint != nil && *insertedHint {
		return
	}
	if strings.EqualFold(strings.TrimSpace(state), "READY") {
		return
	}

	p.simEventMu.Lock()
	if p.simEventTimers == nil {
		p.simEventTimers = make(map[string]*time.Timer)
	}
	if timer := p.simEventTimers[deviceID]; timer != nil {
		timer.Stop()
	}
	p.simEventTimers[deviceID] = time.AfterFunc(1500*time.Millisecond, func() {
		p.confirmSIMRemovedAndStopVoWiFi(deviceID, source)
	})
	p.simEventMu.Unlock()
}

var deviceEventRecoverWakeDelay = 500 * time.Millisecond

type deviceEventRecoverWakeup struct {
	timer   *time.Timer
	sources map[string]struct{}
}

func (p *Pool) wakeDesiredVoWiFiRecoverFromDeviceEvent(deviceID, reason string) {
	if p == nil || p.ctx.Err() != nil {
		return
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = vowifiDesiredReconcileReason
	}

	delay := deviceEventRecoverWakeDelay
	if delay < 0 {
		delay = 0
	}
	p.deviceEventWakeMu.Lock()
	if p.deviceEventWakeups == nil {
		p.deviceEventWakeups = make(map[string]*deviceEventRecoverWakeup)
	}
	state := p.deviceEventWakeups[deviceID]
	if state == nil {
		state = &deviceEventRecoverWakeup{sources: make(map[string]struct{})}
		p.deviceEventWakeups[deviceID] = state
	}
	state.sources[reason] = struct{}{}
	if state.timer == nil {
		state.timer = time.AfterFunc(delay, func() {
			p.flushDesiredVoWiFiRecoverFromDeviceEvent(deviceID)
		})
	} else {
		state.timer.Reset(delay)
	}
	p.deviceEventWakeMu.Unlock()
}

func (p *Pool) flushDesiredVoWiFiRecoverFromDeviceEvent(deviceID string) {
	if p == nil || p.ctx.Err() != nil {
		return
	}
	p.deviceEventWakeMu.Lock()
	state := p.deviceEventWakeups[deviceID]
	if state == nil {
		p.deviceEventWakeMu.Unlock()
		return
	}
	delete(p.deviceEventWakeups, deviceID)
	sources := make([]string, 0, len(state.sources))
	for source := range state.sources {
		sources = append(sources, source)
	}
	p.deviceEventWakeMu.Unlock()
	if len(sources) == 0 {
		return
	}
	sort.Strings(sources)
	reason := sources[0]
	if len(sources) > 1 {
		reason = "post_switch_event_wakeup"
		logger.Debug("VoWiFi 事件唤醒恢复已合并",
			"device", deviceID,
			"sources", sources,
			"reason", reason)
	}

	p.mu.RLock()
	worker := p.workers[deviceID]
	p.mu.RUnlock()
	shouldRecover := p.shouldReconcileVoWiFiForReason(worker, reason)
	if !shouldRecover {
		return
	}
	p.scheduleDesiredVoWiFiRecover(deviceID, reason, time.Now())
}

func (p *Pool) confirmSIMRemovedAndStopVoWiFi(deviceID, source string) {
	p.simEventMu.Lock()
	delete(p.simEventTimers, deviceID)
	p.simEventMu.Unlock()

	if p.IsESIMSwitching(deviceID) {
		logger.Info("SIM 状态变化发生在 eSIM 切换中，跳过 VoWiFi 自动停止", "device", deviceID, "source", source)
		return
	}
	w := p.GetWorker(deviceID)
	if w == nil || w.Backend == nil {
		return
	}

	baseCtx := p.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(baseCtx, 3*time.Second)
	defer cancel()
	inserted, err := w.Backend.IsSimInserted(ctx)
	if err != nil {
		logger.Warn("SIM 拔卡确认失败，跳过 VoWiFi 自动停止", "device", deviceID, "source", source, "err", err)
		return
	}
	if inserted {
		_ = w.RefreshRuntime(ctx, "sim_status_inserted")
		return
	}

	_ = w.RefreshRuntime(ctx, "sim_removed")
	p.clearDesiredVoWiFiRecoverState(deviceID)
	if !p.IsVoWiFiActive(deviceID) {
		return
	}
	logger.Warn("检测到 SIM 已拔出，自动停止 VoWiFi", "device", deviceID, "source", source)
	if err := p.voWiFiHost().Disable(baseCtx, deviceID, "sim_removed", false); err != nil {
		logger.Warn("SIM 拔出后停止 VoWiFi 失败", "device", deviceID, "source", source, "err", err)
	}
}

func (p *Pool) bindESIMUIMIndications(worker *Worker) {
	if worker == nil || worker.QMICore == nil {
		return
	}

	worker.QMICore.OnUIMRefresh(func(info *qmi.UIMRefreshIndication) {
		fileCount := 0
		stage := uint8(0)
		if info != nil {
			fileCount = len(info.Files)
			stage = info.Stage
		}
		logger.Debug("收到 UIM refresh 指示",
			"device", worker.ID,
			"stage", stage,
			"file_count", fileCount)
		if !workerAcceptsRuntimeUIMIndication(worker) {
			logger.Debug("QMI 启动期 UIM refresh 指示已延后处理",
				"device", worker.ID,
				"stage", stage,
				"file_count", fileCount)
			return
		}
		if worker.EsimMgr != nil {
			worker.EsimMgr.NotifyUIMIndication("refresh")
		}
		if src := worker.currentSwitchEventSource(); src != nil {
			src.PublishRefresh(stage)
		}
		p.wakeDesiredVoWiFiRecoverFromDeviceEvent(worker.ID, "post_switch_uim_refresh")
	})

	worker.QMICore.OnUIMSlotStatus(func(info *qmi.UIMSlotStatus) {
		slotCount := 0
		if info != nil {
			slotCount = len(info.Slots)
		}
		logger.Debug("收到 UIM 卡槽状态指示", "device", worker.ID, "slot_count", slotCount)
		if !workerAcceptsRuntimeUIMIndication(worker) {
			logger.Debug("QMI 启动期 UIM 卡槽状态指示已延后处理",
				"device", worker.ID,
				"slot_count", slotCount)
			return
		}
		if worker.EsimMgr != nil {
			worker.EsimMgr.NotifyUIMIndication("slot_status")
		}
		if src := worker.currentSwitchEventSource(); src != nil {
			src.PublishSlotStatus()
		}
		p.wakeDesiredVoWiFiRecoverFromDeviceEvent(worker.ID, "post_switch_uim_slot_status")
	})
}

func workerAcceptsRuntimeUIMIndication(worker *Worker) bool {
	return worker != nil && worker.uimIndicationsReady.Load()
}

func (p *Pool) RemoveWorker(deviceID string) error {
	p.mu.Lock()
	worker := p.workers[deviceID]
	alreadyRebuilding := p.rebuilding[deviceID]
	if worker != nil {
		delete(p.workers, deviceID)
		if !alreadyRebuilding {
			p.rebuilding[deviceID] = true
		}
	}
	p.mu.Unlock()

	if worker == nil && alreadyRebuilding {
		if !p.waitWorkerInitSettled(deviceID, 10*time.Second) {
			return fmt.Errorf("设备 %s 正在初始化中，等待停止超时", deviceID)
		}
		return p.RemoveWorker(deviceID)
	}
	if worker == nil {
		return fmt.Errorf("设备未找到")
	}
	if !alreadyRebuilding {
		defer func() {
			p.mu.Lock()
			delete(p.rebuilding, deviceID)
			p.mu.Unlock()
		}()
	}

	// 移除 Worker 时，使当前设备的 VoWiFi 运行态失效，防止未完成的旧启动例程回写状态
	p.voWiFiHost().InvalidateRuntime(deviceID, "remove_worker")
	if p.stopVoWiFiAppForTeardown(p.ctx, deviceID, "remove") {
		logger.Info("设备移除时强制关闭并清理残留的 VoWiFi 实例", "device", deviceID)
	}

	worker.stopOnce.Do(func() {
		if worker.stop != nil {
			close(worker.stop)
		}
	})

	if worker.publicIPRetryTimer != nil {
		worker.publicIPRetryTimer.Stop()
	}

	if worker.Proxy != nil {
		worker.Proxy.Shutdown()
	}
	if worker.ESIMQMITransport != nil {
		_ = worker.ESIMQMITransport.Stop()
	}
	if worker.QMICore != nil {
		worker.QMICore.Stop()
	}
	if worker.MBIMCore != nil {
		_ = worker.MBIMCore.Close()
	}
	if worker.Backend != nil {
		_ = worker.Backend.Close()
	}
	if worker.Modem != nil {
		if !worker.Modem.StopAndWait(2 * time.Second) {
			logger.Warn("设备移除时等待 AT 管理器退出超时", "device", deviceID)
		}
	}
	if p.lifecycle != nil {
		snap := p.lifecycle.GetSnapshot(deviceID)
		if !snap.Recovering && snap.Phase != LifecyclePhaseEvicting {
			p.lifecycle.MarkOffline(deviceID, "worker_removed")
		}
	}
	return nil
}

// qmiWorkerBootstrapDeadline 是 AddWorkerFromConfig 单次执行的硬上限。
// 内部多数探测都带有名义上的 context 超时，但一旦某一层（包括 vendored QMI 库）
// 未正确响应取消，整条同步链路可能永久卡死，导致设备槽位既无法重试也无法删除。
// 看门狗以此为界强制兜底释放 rebuilding 标记。
var qmiWorkerBootstrapDeadline = 90 * time.Second

// beginRebuildAttemptLocked 标记设备进入新一轮启动/重建尝试，返回本次尝试的 token。
// 调用前必须已持有 p.mu 写锁。
func (p *Pool) beginRebuildAttemptLocked(deviceID string) uint64 {
	p.rebuildAttempt[deviceID]++
	return p.rebuildAttempt[deviceID]
}

// isRebuildAttemptCurrent 判断给定 token 是否仍是该设备最新一次启动尝试。
func (p *Pool) isRebuildAttemptCurrent(deviceID string, attempt uint64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.rebuildAttempt[deviceID] == attempt
}

// endRebuildAttemptIfCurrent 仅在 token 仍是该设备最新一次尝试时才清除 rebuilding 标记，
// 防止看门狗已强制放行后，滞后完成的旧启动流程误清新一轮尝试的状态。
func (p *Pool) endRebuildAttemptIfCurrent(deviceID string, attempt uint64) {
	p.mu.Lock()
	if p.rebuildAttempt[deviceID] == attempt {
		delete(p.rebuilding, deviceID)
	}
	p.mu.Unlock()
}

// startBootstrapWatchdog 为单次 AddWorkerFromConfig 执行设置硬上限看门狗。
// 如果启动流程在 deadline 内既没有成功完成、也没有被更新的尝试取代，
// 看门狗会强制释放 rebuilding 标记，让设备重新可以被重试或删除；
// 调用方应在自身正常返回时 close 掉返回的 stop channel 以避免看门狗误触发日志。
func (p *Pool) startBootstrapWatchdog(deviceID string, attempt uint64, deadline time.Duration) chan struct{} {
	stop := make(chan struct{})
	go func() {
		timer := time.NewTimer(deadline)
		defer timer.Stop()
		select {
		case <-stop:
			return
		case <-p.ctx.Done():
			return
		case <-timer.C:
		}
		p.mu.Lock()
		isCurrent := p.rebuildAttempt[deviceID] == attempt
		if isCurrent {
			delete(p.rebuilding, deviceID)
		}
		p.mu.Unlock()
		if isCurrent {
			logger.Warn("QMI worker 启动看门狗超时，强制释放 rebuilding 标记，设备可能仍在后台初始化",
				"device", deviceID,
				"deadline", deadline.String())
		}
	}()
	return stop
}

func (p *Pool) waitWorkerInitSettled(deviceID string, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		p.mu.RLock()
		worker := p.workers[deviceID]
		rebuilding := p.rebuilding[deviceID]
		p.mu.RUnlock()
		if worker != nil || !rebuilding {
			return true
		}

		select {
		case <-p.ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-ticker.C:
		}
	}
}

func (p *Pool) refreshIdentityAndApplyCardPolicy(worker *Worker, reason string) (liveSIMIdentityRefreshResult, error) {
	if worker == nil {
		return liveSIMIdentityRefreshResult{}, nil
	}

	_ = worker.RefreshRuntime(nil, reason)
	result, identityErr := worker.refreshIdentityLive(nil, reason)

	if p != nil {
		p.PersistRuntimeState(worker)
		p.PersistIdentityState(worker)

		if identityErr == nil {
			if worker.CurrentICCID() != "" {
				p.resolveAndApplyPolicy(worker, reason)
			}
		}

		p.broadcastVoWiFiStateChange(worker.ID)
	}
	return result, identityErr
}

func (p *Pool) applyNetworkPreference(worker *Worker) error {
	if worker == nil {
		return fmt.Errorf("worker 不存在")
	}
	nc := worker.NetworkController()
	if nc == nil {
		if worker.Config.NetworkEnabled {
			return fmt.Errorf("当前设备缺少数据面能力")
		}
		return nil
	}

	if worker.Config.NetworkEnabled {
		if worker.MBIMCore != nil {
			if err := worker.EnsureMBIMRegistration(p.ctx, true); err != nil {
				return err
			}
		} else if worker.QMICore != nil {
			if err := worker.EnsureQMIRegistration(p.ctx, true); err != nil {
				return err
			}
		}
		if p.IsVoWiFiActive(worker.ID) {
			logger.Info("设备当前处于 VoWiFi 模式，跳过自动连接数据网络", "device", worker.ID)
			return nil
		}
		if nc.IsConnected() {
			p.refreshIPs(worker, true)
			return nil
		}
		if err := worker.StartNetwork(); err != nil {
			return err
		}
		p.refreshIPs(worker, true)
		return nil
	}

	if worker.MBIMCore != nil {
		worker.StartMBIMRegistrationReconcile(p.ctx, "network_disabled_preference")
	} else {
		worker.StartQMIRegistrationReconcile(p.ctx, "network_disabled_preference")
	}
	if !nc.IsConnected() {
		worker.clearCachedIP()
		return nil
	}
	return worker.StopNetwork()
}

type existingQMIDataConnectionResetter interface {
	ResetExistingDataConnection(context.Context) (bool, error)
}

func resetExistingQMIDataConnectionBeforePreference(ctx context.Context, deviceID string, reason string, resetter existingQMIDataConnectionResetter) (bool, error) {
	if resetter == nil {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return resetter.ResetExistingDataConnection(ctx)
}

func (p *Pool) resetExistingQMIDataConnectionBeforePreference(worker *Worker, reason string) (bool, error) {
	if worker == nil || worker.QMICore == nil {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	reset, err := resetExistingQMIDataConnectionBeforePreference(ctx, worker.ID, reason, worker.QMICore)
	if err != nil {
		logger.Warn("QMI 初始化后清理已有数据连接失败",
			"device", worker.ID,
			"reason", reason,
			"network_enabled", worker.Config.NetworkEnabled,
			"err", err)
		return false, err
	}
	if reset {
		worker.clearCachedIP()
		logger.Info("QMI 初始化时已清理既有数据连接",
			"device", worker.ID,
			"reason", reason,
			"network_enabled", worker.Config.NetworkEnabled)
	}
	return reset, nil
}

func (p *Pool) ApplyConfiguredNetwork(deviceID string) error {
	worker := p.GetWorker(deviceID)
	if worker == nil {
		return fmt.Errorf("设备 %s 未找到", deviceID)
	}
	return p.applyNetworkPreference(worker)
}

func shouldStartConfiguredQMIWithoutIMEIMatch(cfg config.DeviceConfig, staticQMIIndex StaticQMIDeviceIndex, discoveryAvailable bool) bool {
	if !requiresQMICore(cfg) {
		return false
	}
	controlPath := strings.TrimSpace(cfg.ControlDevice)
	if controlPath == "" {
		controlPath = strings.TrimSpace(cfg.QMIDevice)
	}
	if controlPath == "" && strings.TrimSpace(cfg.USBPath) == "" && strings.TrimSpace(cfg.Interface) == "" {
		return false
	}
	if !discoveryAvailable {
		return controlPath != ""
	}
	_, ok := staticQMIIndex.Lookup(controlPath, cfg.USBPath, cfg.Interface)
	return ok
}

func (p *Pool) StartAll() error {
	if p == nil || p.cfg == nil {
		return nil
	}
	p.startPoolBackgroundServicesOnce()

	devices := append([]config.DeviceConfig(nil), p.cfg.Devices...)
	for i := range devices {
		devCfg := devices[i]
		if !FreeDeviceLimitAllowsConfiguredDevice(devices, devCfg.ID) {
			logger.Warn("当前版本设备数量限制，跳过启动配置设备",
				"device", devCfg.ID,
				"limit", DefaultFreeDeviceLimit)
			continue
		}
		go p.startConfiguredDeviceBootstrap(devCfg, "start_all")
	}
	return nil
}

func (p *Pool) startPoolBackgroundServicesOnce() {
	if p == nil {
		return
	}
	p.startOnce.Do(func() {
		go p.healthCheckLoop()
		go p.overviewStreamLoop()
		go p.startVoWiFiDesiredReconcileLoop()
		p.startInitialDesiredVoWiFiAutoStart(5 * time.Second)

		p.udevWatcher = NewUdevWatcher(p)
		p.udevWatcher.Start()
	})
}

func (p *Pool) startConfiguredDeviceBootstrap(devCfg config.DeviceConfig, reason string) {
	if p == nil {
		return
	}
	select {
	case <-p.ctx.Done():
		return
	default:
	}
	if _, err := p.AddWorkerFromConfig(devCfg); err != nil {
		logger.Warn("配置设备异步启动失败，等待健康检查或重扫恢复",
			"device", devCfg.ID,
			"reason", reason,
			"err", err)
	}
}

func (p *Pool) bindModemReadyIndications(worker *Worker) {
	if worker == nil || worker.Modem == nil {
		return
	}
	go func(w *Worker) {
		for {
			ch := w.Modem.SubscribeRDY()
			select {
			case <-p.ctx.Done():
				return
			case <-w.stop:
				return
			case <-ch:
				logger.Info("[事件驱动] Modem RDY（切卡补刷新已禁用）", "device", w.ID)
				p.wakeDesiredVoWiFiRecoverFromDeviceEvent(w.ID, "post_switch_modem_ready")
			}
		}
	}(worker)
}

func qmiManagedAttachmentChanged(cfg config.DeviceConfig, dev QMIDevice) bool {
	if v := strings.TrimSpace(dev.ControlPath); v != "" && v != strings.TrimSpace(cfg.ControlDevice) {
		return true
	}
	if v := strings.TrimSpace(dev.NetInterface); v != "" && v != strings.TrimSpace(cfg.Interface) {
		return true
	}
	if v := strings.TrimSpace(dev.USBPath); v != "" && v != strings.TrimSpace(cfg.USBPath) {
		return true
	}
	return false
}

func applyQMIManagedAttachment(cfg config.DeviceConfig, dev QMIDevice) config.DeviceConfig {
	if v := strings.TrimSpace(dev.ControlPath); v != "" {
		cfg.ControlDevice = v
		cfg.QMIDevice = v
	}
	if v := strings.TrimSpace(dev.NetInterface); v != "" {
		cfg.Interface = v
	}
	if v := strings.TrimSpace(dev.USBPath); v != "" {
		cfg.USBPath = v
	}
	if v := strings.TrimSpace(dev.ATPort); v != "" {
		cfg.ATPort = v
		cfg.ManagePort = v
	}
	if v := strings.TrimSpace(dev.AudioDevice); v != "" {
		cfg.AudioDevice = v
	}
	return cfg
}

func qmiHealthyWorkerAttachmentUpdate(worker *Worker, live QMIDevice) (bool, config.DeviceConfig) {
	if worker == nil {
		return false, config.DeviceConfig{}
	}
	cfg := worker.Config
	if !requiresQMICore(cfg) {
		return false, cfg
	}
	if !qmiManagedAttachmentChanged(cfg, live) {
		return false, cfg
	}
	return true, applyQMIManagedAttachment(cfg, live)
}

type rescanReconnectOptions struct {
	targetDeviceID string
	manualReboot   bool
}

func (opts rescanReconnectOptions) allowWorkerMutation(deviceID string) bool {
	if !opts.manualReboot || strings.TrimSpace(opts.targetDeviceID) == "" {
		return true
	}
	return strings.TrimSpace(deviceID) == strings.TrimSpace(opts.targetDeviceID)
}

// RescanAndReconnect 重新扫描硬件设备并根据 IMEI 自动重连
// 用于热插拔场景：设备插入后自动启动对应 Worker，设备拔出后标记离线
func (p *Pool) RescanAndReconnect() error {
	return p.rescanAndReconnect(rescanReconnectOptions{})
}

func (p *Pool) collectRescanHardware(discovered []QMIDevice, liveWorkerIndex WorkerDiscoveryIndex) []CompatibleModem {
	var hardware []CompatibleModem
	for i := range discovered {
		raw := discovered[i]
		var imei string
		if liveInfo, ok := liveWorkerIndex.Lookup(raw.ControlPath, raw.USBPath, raw.NetInterface); ok {
			if containsPort(raw.ATPorts, liveInfo.ATPort) {
				raw.ATPort = liveInfo.ATPort
			}
			if liveInfo.IMEI != "" {
				imei = liveInfo.IMEI
				logger.Debug("扫描到设备", "imei", liveInfo.IMEI, "interface", raw.NetInterface, "at", raw.ATPort)
			} else {
				raw, imei = resolveDiscoveredQMIDeviceFn(raw, 1600*time.Millisecond, true)
			}
		} else {
			raw, imei = resolveDiscoveredQMIDeviceFn(raw, 1600*time.Millisecond, true)
		}
		hardware = append(hardware, CompatibleModem{
			IMEI:          imei,
			ControlPath:   raw.ControlPath,
			NetInterface:  raw.NetInterface,
			USBPath:       raw.USBPath,
			ATPort:        raw.ATPort,
			TransportType: backend.BackendQMI,
			Mode:          "qmi",
		})
	}

	managed := config.ListDevices()
	if configuredDevicesNeedCompatibleATDiscovery(managed) {
		if compatList, err := DiscoverCompatibleModemsFromQMI(discovered); err == nil {
			seen := map[string]bool{}
			for _, hw := range hardware {
				if k := config.NormalizeIMEI(hw.IMEI); k != "" {
					seen[k] = true
				}
			}
			for _, raw := range compatList {
				m, imei := resolveDiscoveredCompatibleModemFn(raw, 1200*time.Millisecond)
				if config.NormalizeIMEI(imei) == "" {
					continue
				}
				if seen[config.NormalizeIMEI(imei)] {
					continue
				}
				m.IMEI = imei
				hardware = append(hardware, m)
			}
		}
	}
	return hardware
}

func (p *Pool) rescanAndReconnect(opts rescanReconnectOptions) error {
	discovered, err := discoverQMIDevicesFn()
	if err != nil {
		logger.Warn("QMI 硬件扫描失败，将继续使用兼容扫描", "err", err)
		discovered = nil
	}

	liveWorkerIndex := BuildWorkerDiscoveryIndex(p.GetAllWorkers(), false)
	hardware := p.collectRescanHardware(discovered, liveWorkerIndex)
	managed := config.ListDevices()
	resolved := ResolveDeviceIdentities(hardware, managed)

	if len(resolved.Degraded) > 0 || len(resolved.Unmatched) > 0 {
		logger.Debug("rescan 发现未匹配或退化设备", "degraded", len(resolved.Degraded), "unmatched", len(resolved.Unmatched))
	}

	for _, pair := range resolved.Matched {
		md := pair.Config
		if !FreeDeviceLimitAllowsConfiguredDevice(managed, md.ID) {
			logger.Warn("当前版本设备数量限制，跳过启动配置设备",
				"device", md.ID,
				"limit", DefaultFreeDeviceLimit)
			continue
		}

		hw := pair.Hardware
		useQMI := requiresQMICore(md)
		worker := p.GetWorker(md.ID)

		if pair.BackfillIMEI != "" {
			md.ModemIMEI = pair.BackfillIMEI
		}

		if worker == nil {
			if !opts.allowWorkerMutation(md.ID) {
				logger.Debug("跳过非目标设备自动启动：当前处于手动重启恢复重扫窗口",
					"device", md.ID,
					"target_device", opts.targetDeviceID)
				continue
			}
			// Worker 不存在，需要启动
			logger.Info("检测到设备上线，自动启动", "device", md.ID, "imei", md.ModemIMEI)
			cfg := md
			if !useQMI {
				if hw.NetInterface != "" {
					cfg.Interface = hw.NetInterface
				}
				cfg.ControlDevice = strings.TrimSpace(hw.ControlPath)
				cfg.QMIDevice = strings.TrimSpace(hw.ControlPath)
				cfg.ATPort = hw.ATPort
				cfg.ManagePort = hw.ATPort
			} else {
				cfg = applyQMIManagedAttachment(cfg, QMIDevice{
					ControlPath:  hw.ControlPath,
					NetInterface: hw.NetInterface,
					USBPath:      hw.USBPath,
					ATPort:       hw.ATPort,
				})
			}
			if p.lifecycle != nil {
				p.lifecycle.BeginRecovery(md.ID, LifecyclePhaseWorkerStarting, "rescan_device_online", qmiLifecycleRecoveryTTL)
			}
			if _, err := p.AddWorkerFromConfig(cfg); err != nil {
				logger.Warn("自动启动设备失败", "device", md.ID, "err", err)
			} else if md.VoWiFiEnabled {
				go func(deviceID string) {
					if err := p.enableVoWiFiWhenReady(deviceID, 5*time.Second, "device_recovery"); err != nil {
						logger.Warn("设备恢复后自动重启 VoWiFi 失败", "device", deviceID, "err", err)
					}
				}(md.ID)
			}
		} else if !worker.IsDeviceHealthy() {
			if !opts.allowWorkerMutation(md.ID) {
				logger.Debug("跳过非目标设备重新初始化：当前处于手动重启恢复重扫窗口",
					"device", md.ID,
					"target_device", opts.targetDeviceID)
				continue
			}
			// Worker 存在但不健康，需要重建
			logger.Info("检测到设备恢复，尝试重新初始化", "device", md.ID, "imei", md.ModemIMEI)
			p.teardownVoWiFiForReconnect(md.ID)
			if p.lifecycle != nil {
				p.lifecycle.BeginRecovery(md.ID, LifecyclePhaseWorkerStarting, "rescan_reinitialize", qmiLifecycleRecoveryTTL)
			}
			_ = p.RemoveWorker(md.ID)
			cfg := md
			if !useQMI {
				if hw.NetInterface != "" {
					cfg.Interface = hw.NetInterface
				}
				cfg.ControlDevice = strings.TrimSpace(hw.ControlPath)
				cfg.QMIDevice = strings.TrimSpace(hw.ControlPath)
				cfg.ATPort = hw.ATPort
				cfg.ManagePort = hw.ATPort
			} else {
				cfg = applyQMIManagedAttachment(cfg, QMIDevice{
					ControlPath:  hw.ControlPath,
					NetInterface: hw.NetInterface,
					USBPath:      hw.USBPath,
					ATPort:       hw.ATPort,
				})
			}
			if _, err := p.AddWorkerFromConfig(cfg); err != nil {
				logger.Warn("重新初始化设备失败", "device", md.ID, "err", err)
			} else if md.VoWiFiEnabled {
				go func(deviceID string) {
					if err := p.enableVoWiFiWhenReady(deviceID, 5*time.Second, "device_recovery"); err != nil {
						logger.Warn("设备恢复后自动重启 VoWiFi 失败", "device", deviceID, "err", err)
					}
				}(md.ID)
			}
		} else {
			// Worker 存在且标记为健康
			hwQMI := QMIDevice{
				ControlPath:  hw.ControlPath,
				NetInterface: hw.NetInterface,
				USBPath:      hw.USBPath,
				ATPort:       hw.ATPort,
			}
			if useQMI {
				if changed, _ := qmiHealthyWorkerAttachmentUpdate(worker, hwQMI); changed {
					if !opts.allowWorkerMutation(md.ID) {
						logger.Debug("跳过非目标设备 QMI 路径变化重建：当前处于手动重启恢复重扫窗口",
							"device", md.ID,
							"target_device", opts.targetDeviceID,
							"old_control", worker.Config.ControlDevice,
							"new_control", hw.ControlPath,
							"old_interface", worker.Config.Interface,
							"new_interface", hw.NetInterface)
						continue
					}
					nextCfg := applyQMIManagedAttachment(md, hwQMI)
					logger.Info("检测到 QMI 设备路径变化，重建 Worker",
						"device", md.ID,
						"old_control", worker.Config.ControlDevice,
						"new_control", nextCfg.ControlDevice,
						"old_interface", worker.Config.Interface,
						"new_interface", nextCfg.Interface,
						"old_usb_path", worker.Config.USBPath,
						"new_usb_path", nextCfg.USBPath)
					p.teardownVoWiFiForReconnect(md.ID)
					if p.lifecycle != nil {
						p.lifecycle.BeginRecovery(md.ID, LifecyclePhaseWorkerStarting, "rescan_qmi_path_change", qmiLifecycleRecoveryTTL)
					}
					if err := p.RemoveWorker(md.ID); err != nil {
						logger.Warn("移除旧 QMI Worker 失败", "device", md.ID, "err", err)
						continue
					}
					if _, err := p.AddWorkerFromConfig(nextCfg); err != nil {
						logger.Warn("使用新 QMI 路径重建 Worker 失败", "device", md.ID, "err", err)
					} else if md.VoWiFiEnabled {
						go func(deviceID string) {
							if err := p.enableVoWiFiWhenReady(deviceID, 5*time.Second, "device_qmi_path_change"); err != nil {
								logger.Warn("QMI 路径变化后自动重启 VoWiFi 失败", "device", deviceID, "err", err)
							}
						}(md.ID)
					}
					continue
				}
			}
			currentATPort := hw.ATPort
			if currentATPort != "" && currentATPort != worker.Config.ATPort {
				logger.Info("检测到设备端口变化，重建 Worker",
					"device", md.ID, "old_port", worker.Config.ATPort, "new_port", currentATPort)
				p.teardownVoWiFiForReconnect(md.ID)
				if p.lifecycle != nil {
					p.lifecycle.BeginRecovery(md.ID, LifecyclePhaseWorkerStarting, "rescan_port_change", qmiLifecycleRecoveryTTL)
				}
				_ = p.RemoveWorker(md.ID)
				cfg := md
				if !useQMI {
					if hw.NetInterface != "" {
						cfg.Interface = hw.NetInterface
					}
					cfg.ControlDevice = strings.TrimSpace(hw.ControlPath)
					cfg.QMIDevice = strings.TrimSpace(hw.ControlPath)
					cfg.ATPort = hw.ATPort
					cfg.ManagePort = hw.ATPort
				} else {
					cfg = applyQMIManagedAttachment(cfg, hwQMI)
				}
				if _, err := p.AddWorkerFromConfig(cfg); err != nil {
					logger.Warn("端口变化后重建设备失败", "device", md.ID, "err", err)
				} else if md.VoWiFiEnabled {
					go func(deviceID string) {
						if err := p.enableVoWiFiWhenReady(deviceID, 5*time.Second, "device_port_change"); err != nil {
							logger.Warn("端口变化后自动重启 VoWiFi 失败", "device", deviceID, "err", err)
						}
					}(md.ID)
				}
			}
		}
	}

	for _, md := range resolved.Offline {
		if !FreeDeviceLimitAllowsConfiguredDevice(managed, md.ID) {
			continue
		}
		worker := p.GetWorker(md.ID)
		if worker != nil {
			logger.Info("检测到设备离线，清理 Worker 以便后续重建",
				"device", md.ID, "imei", md.ModemIMEI,
				"was_healthy", worker.IsDeviceHealthy())
			p.teardownVoWiFiForReconnect(md.ID)
			if p.lifecycle != nil {
				p.lifecycle.BeginRecovery(md.ID, LifecyclePhaseUSBWait, "rescan_device_missing", qmiLifecycleRecoveryTTL)
			}
			_ = p.RemoveWorker(md.ID)
		}
	}

	return nil
}

// RebuildWorker 安全移除并重建指定设备的 Worker。
// 用于需要完全重新初始化的场景（如底盘模式切换或补建 QMI Core）。
func (p *Pool) RebuildWorker(deviceID string) error {
	p.mu.Lock()
	p.rebuilding[deviceID] = true
	p.mu.Unlock()

	// 用于标记是否需要在 defer 中释放（如果提前释放了就置为 false）
	needRelease := true
	defer func() {
		if needRelease {
			p.mu.Lock()
			delete(p.rebuilding, deviceID)
			p.mu.Unlock()
		}
	}()

	cfg, err := config.GetDeviceByID(deviceID)
	if err != nil || cfg == nil {
		return fmt.Errorf("读取设备 %s 配置失败: %w", deviceID, err)
	}
	if !FreeDeviceLimitAllowsConfiguredDevice(config.ListDevices(), cfg.ID) {
		return fmt.Errorf("%s", FreeDeviceWorkerLimitMessage())
	}

	// 先停止 VoWiFi（如有），并让任何正在启动中的旧实例失效。
	p.voWiFiHost().InvalidateRuntime(deviceID, "rebuild_worker")
	if err := p.DisableVoWiFi(deviceID); err != nil {
		logger.Warn("RebuildWorker 停止旧 VoWiFi 失败", "device", deviceID, "err", err)
	}

	// 移除旧 Worker
	if err := p.RemoveWorker(deviceID); err != nil {
		logger.Warn("RebuildWorker 移除旧 Worker 失败", "device", deviceID, "err", err)
	}
	time.Sleep(1 * time.Second) // 等待资源释放

	// 在移交控制权给 AddWorkerFromConfig 之前，主动释放占坑锁。
	// 因为 AddWorkerFromConfig 内部也依赖这把锁来防御健康检查。
	// 虽然有几纳秒的真空期，但这是安全的移交方式。
	p.mu.Lock()
	delete(p.rebuilding, deviceID)
	needRelease = false // defer 无需再释放
	p.mu.Unlock()

	// 重建 Worker
	if _, err := p.AddWorkerFromConfig(*cfg); err != nil {
		return fmt.Errorf("RebuildWorker 重建失败: %w", err)
	}

	if err := p.waitWorkerReady(deviceID, 3*time.Second); err != nil {
		logger.Warn("RebuildWorker 后等待设备恢复健康超时", "device", deviceID, "err", err)
	}

	// 如果配置了 VoWiFi 自动启动
	if cfg.VoWiFiEnabled {
		go func() {
			if err := p.enableVoWiFiWhenReady(deviceID, 5*time.Second, "rebuild_worker"); err != nil {
				logger.Error("RebuildWorker 后 VoWiFi 启动失败", "device", deviceID, "err", err)
			}
		}()
	}

	logger.Info("Worker 重建完成", "device", deviceID)
	return nil
}

func (p *Pool) overviewStreamLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.mu.RLock()
			workers := make([]*Worker, 0, len(p.workers))
			for _, w := range p.workers {
				workers = append(workers, w)
			}
			p.mu.RUnlock()

			var wg sync.WaitGroup
			for _, w := range workers {
				if w != nil && w.StreamSubCount() > 0 {
					wg.Add(1)
					go func(worker *Worker) {
						defer wg.Done()
						_ = worker.RefreshRuntime(nil, "overview_stream")
					}(w)
				}
			}
			wg.Wait()
		}
	}
}

// Pool API 适配
func (p *Pool) GetAllWorkers() []*Worker {
	p.mu.RLock()
	defer p.mu.RUnlock()

	workers := make([]*Worker, 0, len(p.workers))
	for _, w := range p.workers {
		workers = append(workers, w)
	}

	// 固定排序
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].ID < workers[j].ID
	})

	return workers
}

func (p *Pool) GetWorker(id string) *Worker {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.workers[id]
}

func (p *Pool) LifecycleSnapshot(deviceID string) LifecycleSnapshot {
	if p == nil || p.lifecycle == nil {
		return LifecycleSnapshot{Phase: LifecyclePhaseOffline}
	}
	return p.lifecycle.GetSnapshot(deviceID)
}

func (p *Pool) MarkLifecycleRecovery(deviceID string, phase LifecyclePhase, reason string, ttl time.Duration) {
	if p == nil || p.lifecycle == nil {
		return
	}
	p.lifecycle.BeginRecovery(deviceID, phase, reason, ttl)
}

// SetWorkerNetworkPolicy 在锁内同步 worker 运行时的网络策略字段（供热切换显示同步），
// 不触发任何应用动作；返回该 worker（不存在返回 nil）。ipVersion 为空时不改。
func (p *Pool) SetWorkerNetworkPolicy(deviceID string, networkEnabled bool, ipVersion, apn string) *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.workers[deviceID]
	if w == nil {
		return nil
	}
	w.Config.NetworkEnabled = networkEnabled
	if networkEnabled {
		// 开网络与 VoWiFi/飞行互斥（与后端落库互斥保持一致），否则概览仍显示旧模式面板。
		w.Config.VoWiFiEnabled = false
		w.Config.AirplaneEnabled = false
	}
	if strings.TrimSpace(ipVersion) != "" {
		w.Config.IPVersion = strings.TrimSpace(ipVersion)
	}
	w.Config.APN = strings.TrimSpace(apn)
	return w
}

// SetWorkerVoWiFiPolicy 同步 worker 运行时的 VoWiFi 策略字段（供热切换后概览即时反映模式面板）。
// 开 VoWiFi ⇒ airplane=true（射频被 VoWiFi 等效接管）、network=false；
// 关 VoWiFi 仅清 vowifi，不在此清 airplane——airplane 反映用户的纯飞行意图，
// 由随后的 resolveAndApplyPolicy 按当前卡策略重投影回退（之前飞行回飞行，否则回在线）。
func (p *Pool) SetWorkerVoWiFiPolicy(deviceID string, vowifiEnabled bool) *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.workers[deviceID]
	if w == nil {
		return nil
	}
	w.Config.VoWiFiEnabled = vowifiEnabled
	if vowifiEnabled {
		w.Config.AirplaneEnabled = true
		w.Config.NetworkEnabled = false
	}
	return w
}

// SetWorkerAirplanePolicy 同步 worker 运行时的飞行(airplane)策略字段。
// 开飞行 ⇒ vowifi=false、network=false（纯飞行互斥）；关飞行仅清 airplane。
func (p *Pool) SetWorkerAirplanePolicy(deviceID string, airplaneEnabled bool) *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.workers[deviceID]
	if w == nil {
		return nil
	}
	w.Config.AirplaneEnabled = airplaneEnabled
	if airplaneEnabled {
		w.Config.VoWiFiEnabled = false
		w.Config.NetworkEnabled = false
	}
	return w
}

// SetWorkerRoamingPolicy 同步 worker 运行时的漫游策略字段。
func (p *Pool) SetWorkerRoamingPolicy(deviceID string, roamingEnabled bool) *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := p.workers[deviceID]
	if w == nil {
		return nil
	}
	w.Config.RoamingEnabled = roamingEnabled
	return w
}

func (p *Pool) UpdateWorkerConfig(id string, cfg config.DeviceConfig, applyAll bool) bool {
	p.mu.Lock()
	w := p.workers[id]
	if w == nil {
		p.mu.Unlock()
		return false
	}
	if applyAll {
		w.Config = cfg
	} else {
		w.Config.Name = cfg.Name
	}
	p.mu.Unlock()

	p.resolveAndApplyPolicy(w, "config_update")
	return true
}

func newESIMIMEIProvider(w *Worker) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		return w.getIMEIWithContext(ctx), nil
	}
}

func configureWorkerAPDUArbiter(w *Worker, qmiTransport esim.QMIAPDUTransport) {
	if w == nil {
		return
	}
	arbiter := w.APDUArbiter
	if arbiter == nil {
		arbiter = apduarbiter.New(w.Config.ID, apduarbiter.Options{MaxLeaseHold: 10 * time.Minute, MaxSessions: 3, MaxQMITransports: 3})
		w.APDUArbiter = arbiter
	}
	if w.Modem != nil {
		w.Modem.SetAPDUArbiter(arbiter)
	}
	if w.QMICore != nil {
		w.QMICore.SetAPDUArbiter(arbiter)
	}
	if aware, ok := qmiTransport.(apduArbiterAwareTransport); ok {
		aware.SetAPDUArbiter(arbiter)
	}
}

func newESIMManagerForWorker(
	w *Worker,
	qmiTransport esim.QMIAPDUTransport,
	onBefore func(esim.SwitchOperation, string) uint64,
	onAfter func(uint64),
	onFailed func(uint64, error),
	onDegraded func(uint64, esim.SwitchPhase, error),
	onPhase func(uint64, esim.SwitchPhase),
) (*esim.Manager, error) {
	if w == nil {
		return nil, fmt.Errorf("worker 不能为空")
	}

	var beforeWithOperation func(esim.SwitchOperation, string) uint64
	if onBefore != nil {
		beforeWithOperation = func(operation esim.SwitchOperation, targetICCID string) uint64 {
			return onBefore(operation, targetICCID)
		}
	}
	var afterWithOperation func(esim.SwitchOperation, uint64)
	if onAfter != nil {
		afterWithOperation = func(_ esim.SwitchOperation, token uint64) {
			onAfter(token)
		}
	}
	var failedWithOperation func(esim.SwitchOperation, uint64, error)
	if onFailed != nil {
		failedWithOperation = func(_ esim.SwitchOperation, token uint64, err error) {
			onFailed(token, err)
		}
	}
	var degradedWithOperation func(esim.SwitchOperation, uint64, esim.SwitchPhase, error)
	if onDegraded != nil {
		degradedWithOperation = func(_ esim.SwitchOperation, token uint64, phase esim.SwitchPhase, err error) {
			onDegraded(token, phase, err)
		}
	}
	var phaseWithOperation func(esim.SwitchOperation, uint64, esim.SwitchPhase)
	if onPhase != nil {
		phaseWithOperation = func(_ esim.SwitchOperation, token uint64, phase esim.SwitchPhase) {
			onPhase(token, phase)
		}
	}

	mgr, err := esim.NewManager(esim.ManagerOptions{
		DeviceID:             w.Config.ID,
		Transport:            resolveESIMTransport(w.Config, qmiTransport != nil),
		Modem:                w.Modem,
		Backend:              w.Backend,
		QMITransport:         qmiTransport,
		OnBeforeSwitch:       beforeWithOperation,
		OnAfterSwitch:        afterWithOperation,
		OnSwitchFailed:       failedWithOperation,
		OnSwitchDegraded:     degradedWithOperation,
		OnSwitchPhase:        phaseWithOperation,
		APDUArbiter:          w.APDUArbiter,
		PostSwitchMinDelay:   defaultESIMPostSwitchMinDelay,
		SwitchUseRefreshTrue: w.Config.ESIMSwitch.UseRefreshTrue,
	})
	if err != nil {
		return nil, err
	}
	return mgr, nil
}

func (w *Worker) GetStats() map[string]interface{} {
	var rssi int
	if w.Backend != nil {
		if sig, err := w.Backend.GetSignalInfo(context.Background()); err == nil && sig != nil {
			rssi = sig.RSSI
		}
	} else if w.Modem != nil {
		rssi, _ = w.Modem.CheckSignal()
	}
	stats := map[string]interface{}{
		"id":          w.ID,
		"public_ip":   w.GetCachedIP(),
		"public_ipv6": w.GetCachedIPv6(),
		"signal":      rssi,
		"interface":   w.Config.Interface,
	}
	return stats
}

func (w *Worker) GetTrafficStats() map[string]int64 {
	return nil
}

func (w *Worker) Rotate() (oldIP, newIP string, err error) {
	return w.RotateWithNotify()
}

func (w *Worker) StartNetwork() error {
	nc := w.NetworkController()
	if w == nil || nc == nil {
		return fmt.Errorf("network_not_available")
	}
	if w.QMICore != nil {
		if err := w.EnsureQMIRegistration(context.Background(), true); err != nil {
			return err
		}
	} else if w.MBIMCore != nil {
		if err := w.EnsureMBIMRegistration(context.Background(), true); err != nil {
			return err
		}
	}
	return nc.Connect()
}

func (w *Worker) StopNetwork() error {
	nc := w.NetworkController()
	if w == nil || nc == nil {
		return fmt.Errorf("network_not_available")
	}
	if err := nc.Disconnect(); err != nil {
		return err
	}
	w.clearCachedIP()
	return nil
}

func (w *Worker) RotateWithNotify() (oldIP, newIP string, err error) {
	// 防止并发 IP 切换
	w.rotateMu.Lock()
	defer w.rotateMu.Unlock()

	nc := w.NetworkController()
	if nc == nil {
		return "", "", fmt.Errorf("network_not_available")
	}
	if !nc.IsConnected() {
		return "", "", fmt.Errorf("network_not_connected")
	}

	const maxHardRetries = 2
	const publicProbeRetries = 2
	start := time.Now()

	// 1. 并行获取旧 IP (不阻塞主流程)
	oldIPChan := make(chan string, 1)
	go func() {
		// 优先从数据库获取旧 IP (减少一次外网请求)
		var imeiForIP string
		if w.Backend != nil {
			if v, err := w.Backend.GetIMEI(context.Background()); err == nil {
				imeiForIP = v
			}
		}
		if imeiForIP == "" && w.Modem != nil {
			imeiForIP = w.Modem.GetIMEI()
		}
		if imeiForIP != "" {
			if ip, err := db.GetDevicePublicIP(imeiForIP); err == nil && ip != "" {
				oldIPChan <- ip
				return
			}
		}
		oldV4, oldV6 := nc.GetPublicIPv4AndV6NoCache()
		oldIPChan <- representativeIP(oldV4, oldV6)
	}()

	for attempt := 1; attempt <= maxHardRetries; attempt++ {
		// 等待旧 IP 获取完成 (仅第一次)
		if attempt == 1 {
			select {
			case oldIP = <-oldIPChan:
			case <-time.After(publicIPLookupWait):
				if cached := strings.TrimSpace(w.GetCachedIP()); cached != "" {
					oldIP = cached
				} else {
					oldIP = "Unknown"
				}
			}
			logger.Info(fmt.Sprintf("[%s] 请求切换 IP", w.ID), "old_ip", oldIP)
		}

		oldPrivateIP := strings.TrimSpace(nc.GetPrivateIP())

		if err := nc.RotateIP(); err != nil {
			logger.Error(fmt.Sprintf("[%s] IP 切换失败", w.ID), "err", err)

			w.consecutiveFailures++
			if w.consecutiveFailures >= 5 {
				logger.Error(fmt.Sprintf("[%s] 连续切换 IP 失败 5 次，尝试重启模组", w.ID))
				if err := w.Backend.Reboot(context.Background()); err != nil {
					logger.Error(fmt.Sprintf("[%s] 重启模组失败", w.ID), "err", err)
				}
				w.consecutiveFailures = 0
			}
			continue
		}
		w.consecutiveFailures = 0 // 重置失败计数
		newPrivateIP := strings.TrimSpace(nc.GetPrivateIP())

		// 4. 快速探测新外网 IP（最多探测两次，避免 rotate API 长时间阻塞）
		for j := 0; j < publicProbeRetries; j++ {
			publicV4, publicV6 := nc.GetPublicIPv4AndV6NoCache()
			newIP = representativeIP(publicV4, publicV6)
			if newIP != "" && newIP != "Unknown" && newIP != oldIP {
				duration := time.Since(start)
				logger.Info(fmt.Sprintf("[%s] 切换 IP 成功", w.ID), "new_ip", newIP, "duration", duration.String())
				w.Pool.NotifyIPChanged(w.ID, oldIP, newIP, duration)
				// 更新内存缓存
				w.cacheMu.Lock()
				if publicV4 != "" {
					w.cachedIP = publicV4
				}
				if publicV6 != "" {
					w.cachedPublicIPv6 = publicV6
				}
				w.cacheTime = time.Now()
				w.cacheMu.Unlock()

				// 更新数据库中的 IP
				if imei := w.getIMEI(); imei != "" {
					internalIP := nc.GetPrivateIP()
					internalIPv6 := nc.GetPrivateIPv6()
					_ = db.UpdateDeviceIPsV6(imei, publicV4, publicV6, internalIP, internalIPv6)
				}

				if app := w.Pool.voWiFiHost().Instance(w.ID); app != nil {
					logger.Info(fmt.Sprintf("[%s] 指令级 IP 轮换完毕，正平滑触发底层 MOBIKE 漫游", w.ID), "new_ip", newIP)
					if err := app.TriggerMOBIKE(oldIP, newIP); err != nil {
						logger.Warn(fmt.Sprintf("[%s] MOBIKE 漫游触发失败", w.ID), "err", err)
					}
				}

				return oldIP, newIP, nil
			}
			logger.Debug(fmt.Sprintf("[%s] 探测外网 IP 中...", w.ID), "try", j+1)
			time.Sleep(200 * time.Millisecond)
		}

		// 5. 兜底：公网探测可能被限流/拦截，但若私网 IP 已切换则视作成功。
		// 公网 IP 交给后台 refresh 重试机制异步补齐，避免 rotate 误报失败。
		if newPrivateIP != "" && oldPrivateIP != "" && newPrivateIP != oldPrivateIP {
			duration := time.Since(start)
			logger.Info(fmt.Sprintf("[%s] 公网 IP 暂不可得，但私网 IP 已切换，按成功处理", w.ID),
				"old_private_ip", oldPrivateIP,
				"new_private_ip", newPrivateIP,
				"duration", duration.String(),
			)

			// 立即刷新数据库中的私网 IP；公网 IP 先沿用缓存值。
			cachedPublic := w.GetCachedIP()
			if imei := w.getIMEI(); imei != "" {
				_ = db.UpdateDeviceIPsV6(imei, cachedPublic, w.GetCachedIPv6(), newPrivateIP, nc.GetPrivateIPv6())
			}

			// 异步继续探测公网 IP，避免阻塞 rotate 接口。
			w.Pool.refreshIPs(w, true)

			if cachedPublic != "" {
				return oldIP, cachedPublic, nil
			}
			return oldIP, "Unknown", nil
		}
	}

	return oldIP, "Unknown", fmt.Errorf("切换超时")
}

func (p *Pool) NotifyIPChanged(id, oldIP, newIP string, duration time.Duration) {
	notifier := p.getNotifier()
	if notifier == nil {
		return
	}

	// 异步发送通知，不阻塞主流程
	go func() {
		notifier.NotifyIPRotated(id, oldIP, newIP, duration)
	}()
}

func (p *Pool) Shutdown() error {
	// 先关闭所有 VoWiFi 应用实例（确保 XFRMI 接口和 SA/SP 被清理）
	devIDs := p.voWiFiHost().InstanceIDs()
	for _, devID := range devIDs {
		logger.Info("正在关闭 VoWiFi", "device", devID)
		_ = p.stopVoWiFiAppForTeardown(context.Background(), devID, "shutdown")
	}

	p.cancel()
	var wg sync.WaitGroup
	p.mu.RLock()
	for _, w := range p.workers {
		wg.Add(1)
		go func(worker *Worker) {
			defer wg.Done()
			if worker.Proxy != nil {
				logger.Info(fmt.Sprintf("[%s] 正在关闭代理服务器", worker.ID))
				worker.Proxy.Shutdown()
			}
		}(w)

		// 停止 QMI Core
		wg.Add(1)
		go func(worker *Worker) {
			defer wg.Done()
			if worker.QMICore != nil {
				worker.QMICore.Stop()
			}
		}(w)

		wg.Add(1)
		go func(worker *Worker) {
			defer wg.Done()
			if worker.MBIMCore != nil {
				_ = worker.MBIMCore.Close()
			}
		}(w)

		// 停止独立 QMI UIM transport（若存在）
		wg.Add(1)
		go func(worker *Worker) {
			defer wg.Done()
			if worker.ESIMQMITransport != nil {
				_ = worker.ESIMQMITransport.Stop()
			}
		}(w)
	}
	p.mu.RUnlock()

	// 等待 5 秒强制退出
	c := make(chan struct{})
	go func() {
		wg.Wait()
		close(c)
	}()

	select {
	case <-c:
		logger.Info("所有工作器已正常关闭")
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("关闭超时")
	}
}

func (p *Pool) PersistRuntimeState(worker *Worker) {
	if worker == nil {
		return
	}

	status := worker.ProjectDeviceStatus()
	imei := strings.TrimSpace(status.IMEI)

	if imei == "" {
		logger.Warn(fmt.Sprintf("[%s] 无法同步设备信息：IMEI 为空", worker.ID))
		return
	}

	// 更新 Device 表
	if err := db.UpsertDevice(imei, worker.ID, "EC20", worker.Config.ATPort); err != nil {
		logger.Warn(fmt.Sprintf("[%s] 更新设备信息失败", worker.ID), "err", err)
	}
}

func (p *Pool) PersistIdentityState(worker *Worker) {
	if worker == nil {
		return
	}

	status := worker.ProjectDeviceStatus()
	iccid := strings.TrimSpace(status.ICCID)
	imsi := strings.TrimSpace(status.IMSI)
	if iccid == "" {
		return
	}
	imei := strings.TrimSpace(status.IMEI)
	operator := strings.TrimSpace(status.Operator)

	if imei == "" {
		logger.Warn(fmt.Sprintf("[%s] 无法同步设备 SIM 身份：IMEI 为空", worker.ID))
		return
	}

	if err := db.UpsertSIMCard(iccid, imsi, "", operator, &imei); err != nil {
		logger.Warn(fmt.Sprintf("[%s] 更新 SIM 卡信息失败", worker.ID), "err", err)
	}
	if err := db.UpdateDeviceCurrentSIM(imei, &iccid); err != nil {
		logger.Warn(fmt.Sprintf("[%s] 更新设备 SIM 关联失败", worker.ID), "err", err)
	}
	if phone := strings.TrimSpace(worker.getPhoneNumberWithContext(context.Background())); phone != "" {
		if err := db.RecordModemPhoneNumber(imsi, iccid, phone); err != nil {
			logger.Warn(fmt.Sprintf("[%s] 更新调制解调器本机号码失败", worker.ID), "err", err)
		}
	}

	logger.Info(fmt.Sprintf("[%s] 设备 SIM 身份已同步到数据库", worker.ID),
		"imei", imei, "iccid", iccid, "backend", func() string {
			if worker.Backend != nil {
				return worker.Backend.Mode()
			}
			return "none"
		}())
}
