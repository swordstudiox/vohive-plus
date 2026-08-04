// Package mbimcore adapts the pure-Go pkg/mbim protocol stack to vohive's
// backend layer.
package mbimcore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/simaid"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

const defaultMaxControlTransfer = 4096

var (
	errUSSDCanceled = errors.New("mbimcore: USSD canceled")
	errUSSDClosed   = errors.New("mbimcore: USSD closed")
)

type ussdWaiter struct {
	result chan mbim.USSDResponse
	abort  chan error
}

// Manager owns one MBIM control endpoint and a snapshot monitor.
type Manager struct {
	controlDevice string
	transportMode string

	mu                sync.Mutex
	dev               *mbim.Device
	mon               *mbim.Monitor
	smsCB             func()
	simStatusCB       func()
	slotStatusCB      func(slotIndex, state uint32)
	caps              *mbim.Capabilities
	dataCfg           DataConfig
	netcfg            netConfigurator
	desiredConnection bool
	connected         bool
	privateIPv4       string
	privateIPv6       string
	publicIPURLs      []string

	registrationTimeout time.Duration
	connectTimeout      time.Duration
	dataMu              sync.Mutex
	activateRetryDelay  time.Duration
	activateMaxAttempts int

	apduMu      sync.Mutex
	apduArbiter *apduarbiter.Arbiter

	ussdWaiter    *ussdWaiter
	reconnectGate atomic.Bool

	healthProbeInterval time.Duration
	healthProbeTimeout  time.Duration
	healthFailures      int
	healthState         HealthEventState
	healthCB            func(HealthEvent)
	healthDone          chan struct{}
	healthOnce          sync.Once
	healthLoopExited    chan struct{}

	// recovery FSM (control-plane reopen escalation)
	dial              func(mode, devicePath string) (mbim.Transport, error)
	recoveryGate      atomic.Bool
	recoveryExhausted func(reason string, err error)
	triggerReopenHook func(reason string)
	reopenBackoff     time.Duration
	reopenTimeout     time.Duration
	runRecoveryHook   func(reason string)
	setRadioStateHook func(ctx context.Context, sw mbim.RadioSwitch) (mbim.RadioState, error)
}

// New creates an unopened Manager. Call Open before use.
func New(controlDevice, transportMode string) *Manager {
	if transportMode == "" {
		transportMode = "auto"
	}
	return &Manager{
		controlDevice:       controlDevice,
		transportMode:       transportMode,
		publicIPURLs:        append([]string(nil), defaultPublicIPURLs...),
		registrationTimeout: 20 * time.Second,
		connectTimeout:      defaultDataConnectCommandTimeout,
		activateRetryDelay:  time.Second,
		activateMaxAttempts: 4,
		dial:                mbim.Dial,
	}
}

func (m *Manager) ControlDevice() string { return m.controlDevice }

func (m *Manager) SetAPDUArbiter(arbiter *apduarbiter.Arbiter) {
	m.apduMu.Lock()
	m.apduArbiter = arbiter
	m.apduMu.Unlock()
}

// OnNewSMS registers a callback fired when MBIM reports a new short message.
func (m *Manager) OnNewSMS(cb func()) {
	m.mu.Lock()
	m.smsCB = cb
	if m.mon != nil {
		m.mon.SetOnSMS(cb)
	}
	m.mu.Unlock()
}

// OnSimStatusChanged registers a callback fired when the MBIM SIM ready
// state or ICCID changes (insertion/removal/swap).
func (m *Manager) OnSimStatusChanged(cb func()) {
	m.mu.Lock()
	m.simStatusCB = cb
	if m.mon != nil {
		m.mon.SetOnSubscriberReady(func(mbim.Snapshot) { cb() })
	}
	m.mu.Unlock()
}

// OnSlotStatus registers a callback fired on every SLOT_INFO_STATUS indication.
func (m *Manager) OnSlotStatus(cb func(slotIndex, state uint32)) {
	m.mu.Lock()
	m.slotStatusCB = cb
	if m.mon != nil {
		m.mon.SetOnSlotInfoStatus(func(s mbim.SlotInfoStatus) { cb(s.SlotIndex, s.State) })
	}
	m.mu.Unlock()
}

// Open dials and opens the MBIM control endpoint.
func (m *Manager) Open(ctx context.Context) error {
	dialFn := m.dial
	if dialFn == nil {
		dialFn = mbim.Dial
	}
	tr, err := dialFn(m.transportMode, m.controlDevice)
	if err != nil {
		return fmt.Errorf("mbimcore: dial %s: %w", m.controlDevice, err)
	}
	return m.openWithTransport(ctx, tr)
}

func (m *Manager) openWithTransport(ctx context.Context, tr mbim.Transport) error {
	dev := mbim.NewDevice(tr)
	if err := dev.Open(ctx, defaultMaxControlTransfer); err != nil {
		_ = dev.Close()
		return fmt.Errorf("mbimcore: open %s: %w", m.controlDevice, err)
	}
	caps := &mbim.Capabilities{}
	dsCtx, dsCancel := context.WithTimeout(ctx, 3*time.Second)
	ds, dsErr := mbim.QueryDeviceServices(dsCtx, dev)
	dsCancel()
	if dsErr != nil {
		logger.Warn("[mbim] DEVICE_SERVICES 查询失败，能力按空集合继续", "control_device", m.controlDevice, "err", dsErr)
	} else {
		caps.Services = ds
		caps.QMIOverMBIMOK = ds.HasService(mbim.UUIDQMI)
		for _, e := range ds.Elements {
			logger.RunDebug("[mbim] 宣告 service", "control_device", m.controlDevice, "service", e.Service.String(), "cids", e.CIDs)
		}
	}

	if caps.Services.Supports(mbim.UUIDMSBasicConnectExtensions, mbim.CIDMSBasicConnectExtVersion) {
		verCtx, verCancel := context.WithTimeout(ctx, 3*time.Second)
		if devMBIM, devMBIMEx, verErr := mbim.NegotiateVersion(verCtx, dev, mbim.MBIMVersion1_0, mbim.MBIMExVersion2_0); verErr != nil {
			logger.Debug("[mbim] MBIMEx 版本协商失败，按 1.0 行为继续(RSRP/SNR 可能不可用)", "control_device", m.controlDevice, "err", verErr)
		} else {
			caps.MBIMExOK = devMBIMEx >= mbim.MBIMExVersion2_0
			logger.Info("[mbim] MBIMEx 版本协商完成", "control_device", m.controlDevice, "mbim", fmt.Sprintf("0x%04x", devMBIM), "mbimex", fmt.Sprintf("0x%04x", devMBIMEx))
		}
		verCancel()
	}
	logger.Info("[mbim] 能力探针", "control_device", m.controlDevice, "mbimex", caps.MBIMExOK, "auth_advertised", caps.Services.HasService(mbim.UUIDAuth))
	if err := mbim.SubscribeDefaultEvents(ctx, dev); err != nil {
		logger.Warn("[mbim] 事件订阅失败，快照将依赖查询", "err", err)
	}
	m.mu.Lock()
	mon := mbim.NewMonitor(dev)
	if m.smsCB != nil {
		mon.SetOnSMS(m.smsCB)
	}
	if m.simStatusCB != nil {
		mon.SetOnSubscriberReady(func(mbim.Snapshot) { m.simStatusCB() })
	}
	if m.slotStatusCB != nil {
		mon.SetOnSlotInfoStatus(func(s mbim.SlotInfoStatus) { m.slotStatusCB(s.SlotIndex, s.State) })
	}
	mon.SetOnUSSD(m.handleUSSDIndication)
	mon.SetOnConnect(m.handleConnectIndication)
	m.dev, m.mon = dev, mon
	m.caps = caps
	m.healthDone = make(chan struct{})
	m.healthLoopExited = make(chan struct{})
	m.healthOnce = sync.Once{}
	m.healthFailures = 0
	m.healthState = ""
	m.mu.Unlock()
	go mon.Run()
	m.startHealthProbe()
	m.probeUICCCapabilities(caps)
	logger.Info("[mbim] 控制通道已打开", "control_device", m.controlDevice, "transport", m.transportMode)
	return nil
}

func (m *Manager) Capability() *mbim.Capabilities {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.caps
}

func (m *Manager) appListKnownUnsupported() bool {
	m.mu.Lock()
	caps := m.caps
	m.mu.Unlock()
	return caps.AppListKnownUnsupported()
}

func (m *Manager) qmiReadUsable() bool {
	m.mu.Lock()
	caps := m.caps
	m.mu.Unlock()
	return caps != nil && caps.QMIReadUsable()
}

func (m *Manager) device() (*mbim.Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dev == nil {
		return nil, fmt.Errorf("mbimcore: device not opened")
	}
	return m.dev, nil
}

// Close stops the monitor, tears down any active data session, and closes
// the device.
func (m *Manager) Close() error {
	m.dataMu.Lock()
	defer m.dataMu.Unlock()

	m.mu.Lock()
	dev, mon := m.dev, m.mon
	waiter := m.ussdWaiter
	connected := m.connected
	iface := m.dataCfg.Interface
	nc := m.netcfg
	healthDone := m.healthDone
	m.dev, m.mon, m.ussdWaiter = nil, nil, nil
	m.desiredConnection = false
	m.connected = false
	m.privateIPv4, m.privateIPv6 = "", ""
	m.mu.Unlock()
	m.recoveryGate.Store(false)

	notifyUSSDWaiter(waiter, errUSSDClosed)
	if mon != nil {
		mon.Stop()
	}
	if healthDone != nil {
		m.healthOnce.Do(func() { close(healthDone) })
	}
	if connected && dev != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, _ = mbim.Connect(ctx, dev, dataSessionID, mbim.ActivationCommandDeactivate, "", "", "", mbim.AuthProtocolNone, mbim.ContextIPTypeDefault)
		cancel()
		if nc != nil {
			_ = nc.Flush(iface)
		}
	}
	if dev != nil {
		return dev.Close()
	}
	return nil
}

func (m *Manager) SetDataConfig(c DataConfig) {
	m.mu.Lock()
	m.dataCfg = c
	if m.netcfg == nil {
		m.netcfg = realNetConfigurator{}
	}
	m.mu.Unlock()
}

func (m *Manager) DeviceCaps(ctx context.Context) (mbim.Caps, error) {
	d, err := m.device()
	if err != nil {
		return mbim.Caps{}, err
	}
	return mbim.DeviceCaps(ctx, d)
}

func (m *Manager) SubscriberReady(ctx context.Context) (mbim.SubscriberReady, error) {
	d, err := m.device()
	if err != nil {
		return mbim.SubscriberReady{}, err
	}
	return mbim.QuerySubscriberReady(ctx, d)
}

func (m *Manager) RegisterState(ctx context.Context) (mbim.RegisterState, error) {
	return m.GetRegisterState(ctx)
}

func (m *Manager) VisibleProviders(ctx context.Context) ([]mbim.Provider, error) {
	d, err := m.device()
	if err != nil {
		return nil, err
	}
	return mbim.QueryVisibleProviders(ctx, d)
}

func (m *Manager) HomeProvider(ctx context.Context) (mbim.Provider, error) {
	d, err := m.device()
	if err != nil {
		return mbim.Provider{}, err
	}
	return mbim.QueryHomeProvider(ctx, d)
}

func (m *Manager) GetRegisterState(ctx context.Context) (mbim.RegisterState, error) {
	d, err := m.device()
	if err != nil {
		return mbim.RegisterState{}, err
	}
	return mbim.QueryRegisterState(ctx, d)
}

func (m *Manager) SetRegister(ctx context.Context, action uint32, plmn string) (mbim.RegisterState, error) {
	d, err := m.device()
	if err != nil {
		return mbim.RegisterState{}, err
	}
	return mbim.SetRegisterState(ctx, d, action, plmn)
}

func (m *Manager) SignalState(ctx context.Context) (mbim.SignalState, error) {
	d, err := m.device()
	if err != nil {
		return mbim.SignalState{}, err
	}
	return mbim.QuerySignalState(ctx, d)
}

func (m *Manager) PacketService(ctx context.Context) (mbim.PacketService, error) {
	d, err := m.device()
	if err != nil {
		return mbim.PacketService{}, err
	}
	return mbim.QueryPacketService(ctx, d)
}

func (m *Manager) SetPacketService(ctx context.Context, action mbim.PacketServiceAction) (mbim.PacketService, error) {
	d, err := m.device()
	if err != nil {
		return mbim.PacketService{}, err
	}
	return mbim.SetPacketService(ctx, d, action)
}

func (m *Manager) RadioState(ctx context.Context) (mbim.RadioState, error) {
	d, err := m.device()
	if err != nil {
		return mbim.RadioState{}, err
	}
	return mbim.QueryRadioState(ctx, d)
}

func (m *Manager) SetRadioState(ctx context.Context, sw mbim.RadioSwitch) (mbim.RadioState, error) {
	d, err := m.device()
	if err != nil {
		return mbim.RadioState{}, err
	}
	return mbim.SetRadioState(ctx, d, sw)
}

func (m *Manager) DeviceReset(ctx context.Context) error {
	d, err := m.device()
	if err != nil {
		return err
	}
	return mbim.DeviceReset(ctx, d)
}

func (m *Manager) Snapshot() mbim.Snapshot {
	m.mu.Lock()
	mon := m.mon
	m.mu.Unlock()
	if mon == nil {
		return mbim.Snapshot{}
	}
	return mon.Snapshot()
}

func (m *Manager) SendSMS(ctx context.Context, pdu []byte) (uint32, error) {
	d, err := m.device()
	if err != nil {
		return 0, err
	}
	return mbim.SendSMS(ctx, d, pdu)
}

func (m *Manager) ReadSMS(ctx context.Context, index uint32) (mbim.SMSRecord, error) {
	d, err := m.device()
	if err != nil {
		return mbim.SMSRecord{}, err
	}
	return mbim.ReadSMS(ctx, d, index)
}

func (m *Manager) ListSMS(ctx context.Context) ([]mbim.SMSRecord, error) {
	d, err := m.device()
	if err != nil {
		return nil, err
	}
	return mbim.ListSMS(ctx, d)
}

func (m *Manager) DeleteSMS(ctx context.Context, index uint32) error {
	d, err := m.device()
	if err != nil {
		return err
	}
	return mbim.DeleteSMS(ctx, d, index)
}

func (m *Manager) DeleteAllSMS(ctx context.Context) error {
	d, err := m.device()
	if err != nil {
		return err
	}
	return mbim.DeleteAllSMS(ctx, d)
}

func (m *Manager) GetSMSC(ctx context.Context) (string, error) {
	d, err := m.device()
	if err != nil {
		return "", err
	}
	return mbim.GetSMSC(ctx, d)
}

func (m *Manager) SetSMSC(ctx context.Context, smsc string) error {
	d, err := m.device()
	if err != nil {
		return err
	}
	return mbim.SetSMSC(ctx, d, smsc)
}

func (m *Manager) ExecuteUSSD(ctx context.Context, command string, timeout time.Duration) (mbim.USSDResult, error) {
	return m.sendUSSDAndWait(ctx, mbim.USSDActionInitiate, command, timeout)
}

func (m *Manager) ContinueUSSD(ctx context.Context, input string, timeout time.Duration) (mbim.USSDResult, error) {
	return m.sendUSSDAndWait(ctx, mbim.USSDActionContinue, input, timeout)
}

func (m *Manager) sendUSSDAndWait(ctx context.Context, action uint32, text string, timeout time.Duration) (mbim.USSDResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 90 * time.Second
	}

	waiter := &ussdWaiter{
		result: make(chan mbim.USSDResponse, 1),
		abort:  make(chan error, 1),
	}
	m.mu.Lock()
	if m.dev == nil {
		m.mu.Unlock()
		return mbim.USSDResult{}, fmt.Errorf("mbimcore: device not opened")
	}
	if m.ussdWaiter != nil {
		m.mu.Unlock()
		return mbim.USSDResult{}, fmt.Errorf("mbimcore: USSD session already pending")
	}
	d := m.dev
	m.ussdWaiter = waiter
	m.mu.Unlock()
	defer m.clearUSSDWaiter(waiter)

	dcs, payload := mbim.EncodeUSSDRequest(text)
	resp, err := mbim.SendUSSD(ctx, d, action, dcs, payload)
	if err != nil {
		return mbim.USSDResult{}, err
	}
	if len(resp.Payload) > 0 || resp.Response != mbim.USSDRespActionRequired {
		return mbim.NewUSSDResult(resp), nil
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-waiter.result:
		return mbim.NewUSSDResult(resp), nil
	case err := <-waiter.abort:
		return mbim.USSDResult{}, err
	case <-ctx.Done():
		return mbim.USSDResult{}, ctx.Err()
	case <-timer.C:
		return mbim.USSDResult{}, fmt.Errorf("mbimcore: USSD response timeout")
	}
}

func (m *Manager) CancelUSSD(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d, err := m.device()
	if err != nil {
		return err
	}
	_, err = mbim.SendUSSD(ctx, d, mbim.USSDActionCancel, 0, nil)
	if err == nil {
		m.abortUSSDWaiter(errUSSDCanceled)
	}
	return err
}

func (m *Manager) handleUSSDIndication(resp mbim.USSDResponse) {
	m.mu.Lock()
	waiter := m.ussdWaiter
	m.mu.Unlock()
	if waiter == nil {
		return
	}
	select {
	case waiter.result <- resp:
	default:
	}
}

func (m *Manager) clearUSSDWaiter(waiter *ussdWaiter) {
	m.mu.Lock()
	if m.ussdWaiter == waiter {
		m.ussdWaiter = nil
	}
	m.mu.Unlock()
}

func (m *Manager) abortUSSDWaiter(err error) {
	m.mu.Lock()
	waiter := m.ussdWaiter
	if waiter != nil {
		m.ussdWaiter = nil
	}
	m.mu.Unlock()
	notifyUSSDWaiter(waiter, err)
}

func notifyUSSDWaiter(waiter *ussdWaiter, err error) {
	if waiter == nil {
		return
	}
	select {
	case waiter.abort <- err:
	default:
	}
}

func (m *Manager) OpenChannel(ctx context.Context, aid []byte) (uint32, error) {
	d, err := m.device()
	if err != nil {
		return 0, err
	}
	return mbim.UICCOpenChannel(ctx, d, aid)
}

func (m *Manager) CloseChannel(ctx context.Context, channel uint32) error {
	d, err := m.device()
	if err != nil {
		return err
	}
	return mbim.UICCCloseChannel(ctx, d, channel)
}

func (m *Manager) TransmitAPDU(ctx context.Context, channel uint32, command []byte) ([]byte, error) {
	d, err := m.device()
	if err != nil {
		return nil, err
	}
	return mbim.UICCAPDU(ctx, d, channel, command)
}

func (m *Manager) acquireSIMLease(ctx context.Context, owner string, class apduarbiter.APDUClass) (*apduarbiter.Lease, error) {
	m.apduMu.Lock()
	arbiter := m.apduArbiter
	m.apduMu.Unlock()
	if arbiter == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	return arbiter.AcquireTransport(ctx, apduarbiter.Request{
		Owner:   owner,
		Mode:    "MBIM",
		Class:   class,
		Channel: 0,
		Scope:   apduarbiter.TransportScopeExclusive,
	})
}

// CalculateAKA runs MBIM Auth AKA, returning RES/IK/CK/AUTS.
func (m *Manager) CalculateAKA(ctx context.Context, rand, autn []byte) (res, ik, ck, auts []byte, err error) {
	lease, lerr := m.acquireSIMLease(ctx, "mbim_aka", apduarbiter.APDUClassUSIMAKA)
	if lerr != nil {
		return nil, nil, nil, nil, lerr
	}
	if lease != nil {
		defer lease.Release()
	}
	d, derr := m.device()
	if derr != nil {
		return nil, nil, nil, nil, derr
	}
	return mbim.AuthAKA(ctx, d, rand, autn)
}

// AuthSIM runs MBIM Auth SIM (2G GSM) for a single RAND. Diagnostic use: a
// functional Auth subsystem returns SRES/Kc for any RAND (no AUTN/MAC), so this
// discriminates "Auth service is a stub" from "Auth works".
func (m *Manager) AuthSIM(ctx context.Context, rand []byte) (sres uint32, kc uint64, err error) {
	d, derr := m.device()
	if derr != nil {
		return 0, 0, derr
	}
	return mbim.AuthSIM(ctx, d, rand)
}

// usimADFAID is the canonical USIM ADF application identifier.
var usimADFAID = []byte{0xA0, 0x00, 0x00, 0x00, 0x87, 0x10, 0x02}

// isdrBootstrapAID 是 GSMA eUICC 的 ISD-R AID(完整 16 字节),用作开逻辑通道的引导:
// 部分模组只接受完整 AID 开通道。与 internal/sim.ISDRBootstrapAID 一致。
var isdrBootstrapAID = []byte{0xA0, 0x00, 0x00, 0x05, 0x59, 0x10, 0x10, 0xFF, 0xFF, 0xFF, 0xFF, 0x89, 0x00, 0x00, 0x01, 0x00}

// ReadSIMEF 读取 USIM ADF 下的透明 EF:解析完整 AID(APPLICATION_LIST,不支持时
// 回退 EF_DIR 直读扫描)→ READ_BINARY(CID 9)直读,给完整 AID + 2 字节 FID 路径,
// 模组内部完成选应用/选文件。不开逻辑通道、不发裸 APDU、不回退短 AID——这颗
// EM7430 上短 AID 开通道必然以 status=0x87430002(SelectFailed)失败,回退没有
// 意义;READ_BINARY/READ_RECORD 这两个直读 CID 才是真正验证过可用的路径(EF_DIR
// 直读扫描已证明这两个 CID 在这颗卡上确实被实现)。
func (m *Manager) ReadSIMEF(ctx context.Context, fileID uint16, readLen int) ([]byte, error) {
	lease, err := m.acquireSIMLease(ctx, "mbim_read_ef", apduarbiter.APDUClassEUICCRead)
	if err != nil {
		return nil, err
	}
	if lease != nil {
		defer lease.Release()
	}
	d, err := m.device()
	if err != nil {
		return nil, err
	}

	aid, err := m.resolveAppAID(ctx, usimADFAID)
	if err != nil {
		return nil, fmt.Errorf("mbimcore: resolve USIM AID: %w", err)
	}
	size := uint32(readLen)
	if readLen <= 0 || readLen > 256 {
		size = 256
	}
	path := []byte{byte(fileID >> 8), byte(fileID)}
	res, err := mbim.UICCReadBinary(ctx, d, aid, path, 0, size)
	if err != nil {
		var se *mbim.StatusError
		if errors.As(err, &se) && se.Status == 0x9 {
			if !m.qmiReadUsable() {
				return nil, fmt.Errorf("mbimcore: READ_BINARY EF %04X: 设备不支持该 CID，且未探测到 QMI over MBIM 隧道可作兜底: %w", fileID, err)
			}
			logger.Debug("[mbim] UICC_READ_BINARY 返回不支持，回退到 QMI over MBIM READ_TRANSPARENT", "fileID", fmt.Sprintf("%04X", fileID))
			// EF_SPN/EF_AD/EF_GID1/EF_GID2 等都是直接挂在 ADF_USIM 根下的文件：真机验证过
			// 给一段编造的 DF 路径(如 3F00,7FFF)会被拒绝(qmi_error=0x0010 NOT_PROVISIONED)，
			// 路径必须留空；但留空路径的同时还得显式传该应用的完整 AID(下面已经由
			// resolveAppAID 解析好了)，否则模组无法确定当前要在哪个应用上下文里找文件，
			// 同样会拒绝(qmi_error=0x0030 INVALID_ARGUMENT)。
			data, sw1, sw2, qerr := d.QMIReadTransparentEF(ctx, fileID, aid, nil, 0, 0)
			if qerr != nil {
				return nil, fmt.Errorf("mbimcore: QMI READ_TRANSPARENT EF %04X: %w", fileID, qerr)
			}
			if sw1 != 0x90 {
				return nil, fmt.Errorf("mbimcore: QMI READ_TRANSPARENT EF %04X: SW=%02X%02X", fileID, sw1, sw2)
			}
			return data, nil
		}
		return nil, fmt.Errorf("mbimcore: READ_BINARY EF %04X: %w", fileID, err)
	}
	if res.SW1 != 0x90 {
		return nil, fmt.Errorf("mbimcore: READ_BINARY EF %04X: SW=%02X%02X", fileID, res.SW1, res.SW2)
	}
	return res.Data, nil
}

// ReadSIMRecordEF 读取 USIM ADF 下的线性记录 EF:解析完整 AID,使用 READ_RECORD 直读,
// 失败时同样回退到 QMI READ_RECORD。
func (m *Manager) ReadSIMRecordEF(ctx context.Context, fileID uint16, recordNumber uint32) ([]byte, error) {
	lease, err := m.acquireSIMLease(ctx, "mbim_read_record", apduarbiter.APDUClassEUICCRead)
	if err != nil {
		return nil, err
	}
	if lease != nil {
		defer lease.Release()
	}
	d, err := m.device()
	if err != nil {
		return nil, err
	}

	aid, err := m.resolveAppAID(ctx, usimADFAID)
	if err != nil {
		return nil, fmt.Errorf("mbimcore: resolve USIM AID: %w", err)
	}
	path := []byte{byte(fileID >> 8), byte(fileID)}
	res, err := mbim.UICCReadRecord(ctx, d, aid, path, recordNumber)
	if err != nil {
		var se *mbim.StatusError
		if errors.As(err, &se) && se.Status == 0x9 {
			if !m.qmiReadUsable() {
				return nil, fmt.Errorf("mbimcore: READ_RECORD EF %04X: 设备不支持该 CID，且未探测到 QMI over MBIM 隧道可作兜底: %w", fileID, err)
			}
			logger.Debug("[mbim] UICC_READ_RECORD 返回不支持，回退到 QMI over MBIM READ_RECORD", "fileID", fmt.Sprintf("%04X", fileID))
			data, sw1, sw2, qerr := d.QMIReadRecordEF(ctx, fileID, aid, nil, uint16(recordNumber), 0)
			if qerr != nil {
				return nil, fmt.Errorf("mbimcore: QMI READ_RECORD EF %04X: %w", fileID, qerr)
			}
			if sw1 != 0x90 {
				return nil, fmt.Errorf("mbimcore: QMI READ_RECORD EF %04X: SW=%02X%02X", fileID, sw1, sw2)
			}
			return data, nil
		}
		return nil, fmt.Errorf("mbimcore: READ_RECORD EF %04X: %w", fileID, err)
	}
	if res.SW1 != 0x90 {
		return nil, fmt.Errorf("mbimcore: READ_RECORD EF %04X: SW=%02X%02X", fileID, res.SW1, res.SW2)
	}
	return res.Data, nil
}

// ApplicationList 通过 MS UICC APPLICATION_LIST 直读卡上应用(含完整 AID)。
// 如果设备不支持 MBIM 原生应用列表接口(AppListOK=false)，将回退至 QMI over MBIM 隧道。
func (m *Manager) ApplicationList(ctx context.Context) ([]mbim.UICCApplication, error) {
	d, err := m.device()
	if err != nil {
		return nil, err
	}

	// 1. 检查能力：如果并未确知不支持 MBIM ApplicationList，则优先尝试原生 MBIM
	if !m.appListKnownUnsupported() {
		apps, err := mbim.QueryUICCApplicationList(ctx, d)
		if err == nil {
			return apps, nil
		}
		logger.Debug("[mbim] QueryUICCApplicationList failed, falling back to QMI", "err", err)
	}

	// 2. 如果不支持 MBIM 原生，或者原生失败，检查是否支持 QMI over MBIM
	m.mu.Lock()
	caps := m.caps
	m.mu.Unlock()

	if caps != nil && !caps.Services.HasService(mbim.UUIDQMI) {
		// 如果确知连 QMI 都不支持，那就没法 fallback 了。
		return nil, fmt.Errorf("mbim ApplicationList unsupported and no QMI service available for fallback")
	}

	// 3. 回退到 QMI over MBIM 隧道
	qmiApps, err := d.QMIUIMApplicationList(ctx)
	if err != nil {
		return nil, fmt.Errorf("mbim ApplicationList and QMI fallback both failed: %w", err)
	}

	// 将 qmi 的 app 类型转译回 MBIM 层期望的类型。
	// mbimUiccApplicationType(2=USIM,3=ISIM) vs QMI: 1=SIM, 2=USIM, 3=RUIM, 4=CSIM, 5=ISIM
	var out []mbim.UICCApplication
	for _, a := range qmiApps {
		var mbimType uint32
		switch a.Type {
		case 2: // QMI USIM
			mbimType = 2 // MBIM USIM
		case 5: // QMI ISIM
			mbimType = 3 // MBIM ISIM
		case 1: // QMI SIM
			mbimType = 1 // MBIM SIM
		case 3: // QMI RUIM
			mbimType = 4 // MBIM RUIM
		case 4: // QMI CSIM
			mbimType = 5 // MBIM CSIM
		default:
			mbimType = 0 // Unknown
		}
		out = append(out, mbim.UICCApplication{Type: mbimType, AID: a.AID})
	}
	return out, nil
}

// ReadBinary 通过 READ_BINARY 直读透明 EF(给定完整 AID 与文件路径)。
func (m *Manager) ReadBinary(ctx context.Context, aid, filePath []byte, readOffset, readSize uint32) (mbim.UICCFileResult, error) {
	d, err := m.device()
	if err != nil {
		return mbim.UICCFileResult{}, err
	}
	return mbim.UICCReadBinary(ctx, d, aid, filePath, readOffset, readSize)
}

// ReadRecord 通过 READ_RECORD 直读线性记录 EF。
func (m *Manager) ReadRecord(ctx context.Context, aid, filePath []byte, recordNumber uint32) (mbim.UICCFileResult, error) {
	d, err := m.device()
	if err != nil {
		return mbim.UICCFileResult{}, err
	}
	return mbim.UICCReadRecord(ctx, d, aid, filePath, recordNumber)
}

// ResolveAppAID 找出 AID 以 prefix 开头的应用的完整 AID(APPLICATION_LIST 直读,
// 不支持时回退 EF_DIR 扫描)。供 backend.MBIMSource 实现 ResolveSIMAuthAID 使用。
func (m *Manager) ResolveAppAID(ctx context.Context, prefix []byte) ([]byte, error) {
	return m.resolveAppAID(ctx, prefix)
}

// resolveAppAID 找出 AID 以 prefix 开头的应用的完整 AID。优先用 APPLICATION_LIST
// 直读;该 CID 不被固件实现时(如 EM7430,status=0x9 NoDeviceSupport,而非某个 AID
// 选不中),回退到标准 EF_DIR 扫描(3GPP TS 102.221,channel 0,无需开通道)。
func (m *Manager) resolveAppAID(ctx context.Context, prefix []byte) ([]byte, error) {
	d, err := m.device()
	if err != nil {
		return nil, err
	}
	if apps, err := mbim.QueryUICCApplicationList(ctx, d); err == nil {
		for _, a := range apps {
			if bytes.HasPrefix(a.AID, prefix) {
				return a.AID, nil
			}
		}
	} else {
		logger.Debug("[mbim] APPLICATION_LIST 不支持，回退 EF_DIR 扫描", "err", err)
	}

	aid, err := m.resolveAppAIDViaEFDIR(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("mbimcore: 卡上未找到匹配 AID 前缀 %X 的应用: %w", prefix, err)
	}
	return aid, nil
}

// efDirPath 是 EF_DIR 在 MF 下的绝对路径(3GPP TS 102.221,FID 2F00)。
var efDirPath = []byte{0x3F, 0x00, 0x2F, 0x00}

// resolveAppAIDViaEFDIR 用 READ_RECORD(CID 10)直读 EF_DIR(AID 为空、绝对路径
// efDirPath,模组内部完成选 MF→选 EF_DIR→读记录),解析出 AID 以 prefix 开头的
// 应用完整 AID。不开逻辑通道——这颗 EM7430 上开逻辑通道无论空 AID
// (UICC_OPEN_CHANNEL status=0x15 InvalidParameters)还是短 AID
// (status=0x87430002 SelectFailed)都必然失败,而 READ_RECORD/READ_BINARY 这两个
// 直读 CID 此前从未在真机上被实际验证过(此前唯一的调用点排在一个必然失败的 AID
// 解析步骤之后)。
func (m *Manager) resolveAppAIDViaEFDIR(ctx context.Context, prefix []byte) ([]byte, error) {
	d, err := m.device()
	if err != nil {
		return nil, err
	}
	const maxRecords = 32
	for record := uint32(1); record <= maxRecords; record++ {
		res, err := mbim.UICCReadRecord(ctx, d, nil, efDirPath, record)
		if err != nil {
			var se *mbim.StatusError
			if errors.As(err, &se) && se.Status == 0x9 && m.qmiReadUsable() {
				data, sw1, sw2, qerr := d.QMIReadRecordEF(ctx, 0x2F00, nil, []byte{0x00, 0x3F}, uint16(record), 0)
				if qerr != nil {
					return nil, fmt.Errorf("读取 EF_DIR 记录 %d 的 QMI fallback 失败: %w", record, qerr)
				}
				if sw1 == 0x6A && (sw2 == 0x83 || sw2 == 0x82) {
					break
				}
				if sw1 != 0x90 {
					continue
				}
				for _, aid := range simaid.CollectTLVValues(data, 0x4F) {
					if bytes.HasPrefix(aid, prefix) {
						return aid, nil
					}
				}
				continue
			}
			return nil, fmt.Errorf("读取 EF_DIR 记录 %d 失败: %w", record, err)
		}
		if res.SW1 == 0x6A && (res.SW2 == 0x83 || res.SW2 == 0x82) {
			break
		}
		if res.SW1 != 0x90 {
			continue
		}
		for _, aid := range simaid.CollectTLVValues(res.Data, 0x4F) {
			if bytes.HasPrefix(aid, prefix) {
				return aid, nil
			}
		}
	}
	return nil, fmt.Errorf("EF_DIR 未发现匹配前缀 %X 的应用", prefix)
}

// ProbeUICCSupport reports whether the modem supports MS UICC Low Level Access.
func (m *Manager) ProbeUICCSupport(ctx context.Context) bool {
	// 用真实 USIM ADF AID 探测:nil/空 AID 在部分固件(如 EM7430)上不可靠。
	ch, err := m.OpenChannel(ctx, usimADFAID)
	if err == nil {
		_ = m.CloseChannel(ctx, ch)
		return true
	}
	// 即便该 AID 选择失败,只要模组返回的是 MS UICC 服务专有的应用层状态码
	// (SelectFailed/InvalidLogicalChannel/NoLogicalChannels),就说明 UICC Low Level
	// Access 服务本身已支持,应判为支持;只有 NoDeviceSupport 等才算不支持。
	return uiccSupportedFromOpenErr(err)
}

// uiccSupportedFromOpenErr 从 OPEN_CHANNEL 的错误判断 MS UICC 服务是否被支持:
// 命中 MS UICC 专有应用层状态码即视为支持(命令已执行、只是 SELECT/通道层面失败)。
func uiccSupportedFromOpenErr(err error) bool {
	var se *mbim.StatusError
	if errors.As(err, &se) {
		switch se.Status {
		case mbim.StatusMSSelectFailed, mbim.StatusMSNoLogicalChannels, mbim.StatusMSInvalidLogicalChannel:
			return true
		}
	}
	return false
}

func (m *Manager) probeUICCCapabilities(caps *mbim.Capabilities) {
	if caps == nil || !caps.Services.HasService(mbim.UUIDMSUICCLowLevelAccess) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// 逻辑通道探针用完整 ISD-R AID(而非短 USIM AID):部分模组(如 EM7430)只接受
	// 完整 AID 开通道,短/空 AID 必 SelectFailed。能开出通道即表示"逻辑通道 APDU 路径
	// 可用"(AKA 经 EF_DIR 兜底拿完整 USIM AID 后即可走此路)。ISD-R 是各 eUICC 通用完整 AID。
	if ch, err := m.OpenChannel(ctx, isdrBootstrapAID); err == nil {
		caps.UICCChannelOK = true
		_ = m.CloseChannel(ctx, ch)
	}
	if _, err := m.ApplicationList(ctx); err == nil {
		caps.AppListOK = true
	}
	if d, err := m.device(); err == nil {
		// READ_BINARY:透明 EF(EF_ICCID 3F00/2FE2)。
		if res, readErr := mbim.UICCReadBinary(ctx, d, nil, []byte{0x3F, 0x00, 0x2F, 0xE2}, 0, 10); readErr == nil && len(res.Data) > 0 && res.SW1 == 0x90 {
			caps.UICCReadOK = true
		}
		// READ_RECORD:线性记录 EF(EF_DIR 3F00/2F00,记录 1)——AID 解析关键路径。
		if res, recErr := mbim.UICCReadRecord(ctx, d, nil, efDirPath, 1); recErr == nil && len(res.Data) > 0 && res.SW1 == 0x90 {
			caps.UICCRecordOK = true
		}
	}
	logger.Info("[mbim] UICC 能力探针", "control_device", m.controlDevice, "uicc_channel_ok", caps.UICCChannelOK, "app_list_ok", caps.AppListOK, "uicc_read_ok", caps.UICCReadOK, "uicc_record_ok", caps.UICCRecordOK)
}

// ResolveLogicalChannelAID 实现了 sim.LogicalChannelAIDResolver 接口，为 AKA 提供精准选卡。
// 它优先依赖已经增强过的 ApplicationList（原生 MBIM -> QMI 隧道）。
func (m *Manager) ResolveLogicalChannelAID(app string, fallback string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// targetType 使用 MBIM 的标准定义: 2=USIM, 3=ISIM
	targetType := uint32(2)
	if app == "isim" {
		targetType = 3
	}

	// ApplicationList 内部已经做了 MBIM 原生 -> QMI 隧道的智能降级
	apps, err := m.ApplicationList(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to list applications: %w", err)
	}

	for _, a := range apps {
		if a.Type == targetType {
			return fmt.Sprintf("%X", a.AID), "mbim_app_list", nil
		}
	}

	return "", "", fmt.Errorf("app type %s (type=%d) not found on card", app, targetType)
}

// QMIUIMApplicationList 透传调用底层的 QMI over MBIM 应用查询。
func (m *Manager) QMIUIMApplicationList(ctx context.Context) ([]mbim.UICCAppInfo, error) {
	d, err := m.device()
	if err != nil {
		return nil, err
	}
	return d.QMIUIMApplicationList(ctx)
}
