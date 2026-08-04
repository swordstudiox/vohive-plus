package device

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/identity"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
)

type workerStatusBackendStub struct {
	mode                     string
	serving                  *backend.ServingSystem
	simInserted              bool
	nativeMCC                string
	nativeMNC                string
	simMetadata              *backend.SIMMetadata
	opMode                   backend.OperatingMode
	opModeErr                error
	setOpModeCalls           []backend.OperatingMode
	openLogicalChannelCalls  int
	closeLogicalChannelCalls int
	closeLogicalChannels     []int
	closeLogicalChannelErrs  map[int]error
	resolvedSIMAuthAID       string
	resolvedSIMAuthSource    string
	resolveSIMAuthAIDErr     error
}

func (s *workerStatusBackendStub) GetIMEI(ctx context.Context) (string, error)      { return "", nil }
func (s *workerStatusBackendStub) GetIMSI(ctx context.Context) (string, error)      { return "", nil }
func (s *workerStatusBackendStub) GetIMSILive(ctx context.Context) (string, error)  { return "", nil }
func (s *workerStatusBackendStub) GetICCID(ctx context.Context) (string, error)     { return "", nil }
func (s *workerStatusBackendStub) GetMSISDN(ctx context.Context) (string, error)    { return "", nil }
func (s *workerStatusBackendStub) GetICCIDLive(ctx context.Context) (string, error) { return "", nil }
func (s *workerStatusBackendStub) GetRevision(ctx context.Context) (string, error)  { return "", nil }
func (s *workerStatusBackendStub) GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error) {
	return nil, nil
}
func (s *workerStatusBackendStub) GetServingSystem(ctx context.Context) (*backend.ServingSystem, error) {
	if s.serving != nil {
		return s.serving, nil
	}
	return nil, nil
}
func (s *workerStatusBackendStub) IsSimInserted(ctx context.Context) (bool, error) {
	return s.simInserted, nil
}
func (s *workerStatusBackendStub) GetNativeMCCMNC(ctx context.Context) (string, string, error) {
	return s.nativeMCC, s.nativeMNC, nil
}

func (s *workerStatusBackendStub) GetNativeSPN(ctx context.Context) (string, error) {
	return "", nil
}
func (s *workerStatusBackendStub) GetSIMMetadata(ctx context.Context) (*backend.SIMMetadata, error) {
	return s.simMetadata, nil
}
func (s *workerStatusBackendStub) GetSIMMetadataLive(ctx context.Context) (*backend.SIMMetadata, error) {
	return s.simMetadata, nil
}
func (s *workerStatusBackendStub) SendSMS(ctx context.Context, to, body string) error { return nil }
func (s *workerStatusBackendStub) ReadSMS(ctx context.Context, index int) (*backend.SMS, error) {
	return nil, nil
}
func (s *workerStatusBackendStub) DeleteSMS(ctx context.Context, index int) error { return nil }
func (s *workerStatusBackendStub) ListSMS(ctx context.Context) ([]backend.SMSSummary, error) {
	return nil, nil
}
func (s *workerStatusBackendStub) DeleteAllSMS(ctx context.Context) error { return nil }
func (s *workerStatusBackendStub) SetOperatingMode(ctx context.Context, mode backend.OperatingMode) error {
	s.setOpModeCalls = append(s.setOpModeCalls, mode)
	return nil
}
func (s *workerStatusBackendStub) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	return s.opMode, s.opModeErr
}
func (s *workerStatusBackendStub) Reboot(ctx context.Context) error { return nil }
func (s *workerStatusBackendStub) OpenLogicalChannel(ctx context.Context, aid string) (int, error) {
	s.openLogicalChannelCalls++
	return 2, nil
}
func (s *workerStatusBackendStub) CloseLogicalChannel(ctx context.Context, channelID int) error {
	s.closeLogicalChannelCalls++
	s.closeLogicalChannels = append(s.closeLogicalChannels, channelID)
	if s.closeLogicalChannelErrs != nil {
		if err := s.closeLogicalChannelErrs[channelID]; err != nil {
			return err
		}
	}
	return nil
}
func (s *workerStatusBackendStub) TransmitAPDU(ctx context.Context, channelID int, command string) (string, error) {
	return "", nil
}
func (s *workerStatusBackendStub) ResolveSIMAuthAID(ctx context.Context, app string, fallbackAID string) (string, string, error) {
	if s.resolveSIMAuthAIDErr != nil {
		return "", "", s.resolveSIMAuthAIDErr
	}
	if s.resolvedSIMAuthAID == "" {
		return fallbackAID, "fallback_test", nil
	}
	return s.resolvedSIMAuthAID, s.resolvedSIMAuthSource, nil
}
func (s *workerStatusBackendStub) Mode() string {
	if s.mode == "" {
		return backend.BackendAT
	}
	return s.mode
}
func (s *workerStatusBackendStub) Close() error { return nil }

func TestResolvedBackendModePrefersExplicitBackend(t *testing.T) {
	cfg := config.DeviceConfig{
		DeviceBackend: "at",
		ControlDevice: "/dev/cdc-wdm0",
	}

	if got := resolvedBackendMode(cfg); got != "at" {
		t.Fatalf("resolvedBackendMode() = %q, want %q", got, "at")
	}
}

func TestResolvedBackendModeFallsBackToQMIWhenControlDeviceExists(t *testing.T) {
	cfg := config.DeviceConfig{
		ControlDevice: "/dev/cdc-wdm0",
	}

	if got := resolvedBackendMode(cfg); got != "qmi" {
		t.Fatalf("resolvedBackendMode() = %q, want %q", got, "qmi")
	}
}

func TestRequiresQMICoreForQMIBackend(t *testing.T) {
	cfg := config.DeviceConfig{
		DeviceBackend: "qmi",
	}

	if !requiresQMICore(cfg) {
		t.Fatal("expected qmi backend to require QMI core")
	}
}

func TestRequiresQMICoreRespectsExplicitATBackendWithRuntimeQMIPaths(t *testing.T) {
	cfg := config.DeviceConfig{
		DeviceBackend: "at",
		ControlDevice: "/dev/cdc-wdm0",
		Interface:     "wwan0",
		ATPort:        "/dev/ttyUSB2",
	}

	if requiresQMICore(cfg) {
		t.Fatal("explicit at backend must not start QMI core even when runtime QMI paths are present")
	}
}

func TestRequiresQMICoreKeepsLegacyControlDeviceAsQMI(t *testing.T) {
	cfg := config.DeviceConfig{
		ControlDevice: "/dev/cdc-wdm0",
		Interface:     "wwan0",
	}

	if !requiresQMICore(cfg) {
		t.Fatal("legacy config with control device and no explicit backend must still require QMI core")
	}
}

type startupUIMResetterStub struct {
	calls int
	err   error
}

func (s *startupUIMResetterStub) UIMReset(ctx context.Context) error {
	s.calls++
	return s.err
}

type startupSIMStatusStub struct {
	statuses []qmi.SIMStatus
	calls    int
}

func (s *startupSIMStatusStub) GetSIMStatus(ctx context.Context) (qmi.SIMStatus, error) {
	s.calls++
	if len(s.statuses) == 0 {
		return qmi.SIMReady, nil
	}
	status := s.statuses[0]
	s.statuses = s.statuses[1:]
	return status, nil
}

func TestStartupQMISIMReadyCheckRequiresSIMReadyStatus(t *testing.T) {
	source := &startupSIMStatusStub{statuses: []qmi.SIMStatus{qmi.SIMNotReady, qmi.SIMReady}}
	check := startupQMISIMReadyCheck(source)

	ready, err := check(context.Background())
	if err != nil {
		t.Fatalf("first ready check error: %v", err)
	}
	if ready {
		t.Fatal("first ready check = true, want false for SIMNotReady")
	}

	ready, err = check(context.Background())
	if err != nil {
		t.Fatalf("second ready check error: %v", err)
	}
	if !ready {
		t.Fatal("second ready check = false, want true for SIMReady")
	}
}

func TestPerformStartupQMIUIMResetWaitsUntilSIMReady(t *testing.T) {
	resetter := &startupUIMResetterStub{}
	readySeq := []bool{false, false, true}
	readyCalls := 0

	ok := performStartupQMIUIMReset("dev-qmi", resetter, nil, func(ctx context.Context) (bool, error) {
		readyCalls++
		if readyCalls > len(readySeq) {
			return true, nil
		}
		return readySeq[readyCalls-1], nil
	}, 100*time.Millisecond, time.Millisecond)

	if !ok {
		t.Fatal("performStartupQMIUIMReset() = false, want true")
	}
	if resetter.calls != 1 {
		t.Fatalf("UIMReset calls = %d, want 1", resetter.calls)
	}
	if readyCalls != 3 {
		t.Fatalf("ready calls = %d, want 3", readyCalls)
	}
}

func TestPerformStartupQMIUIMResetSkipsReadyWaitWhenResetFails(t *testing.T) {
	resetter := &startupUIMResetterStub{err: fmt.Errorf("reset failed")}
	readyCalls := 0

	ok := performStartupQMIUIMReset("dev-qmi", resetter, nil, func(ctx context.Context) (bool, error) {
		readyCalls++
		return true, nil
	}, 100*time.Millisecond, time.Millisecond)

	if ok {
		t.Fatal("performStartupQMIUIMReset() = true, want false")
	}
	if resetter.calls != 1 {
		t.Fatalf("UIMReset calls = %d, want 1", resetter.calls)
	}
	if readyCalls != 0 {
		t.Fatalf("ready calls = %d, want 0", readyCalls)
	}
}

func TestCleanupWorkerStartupSIMAuthLogicalChannelsATFallbackClosesOneThroughFour(t *testing.T) {
	backendStub := &workerStatusBackendStub{mode: backend.BackendAT}
	worker := &Worker{
		ID:      "dev-at",
		Config:  config.DeviceConfig{ID: "dev-at", DeviceBackend: backend.BackendAT},
		Backend: backendStub,
	}

	cleanupWorkerStartupSIMAuthLogicalChannels(worker)

	want := []int{1, 2, 3, 4}
	if len(backendStub.closeLogicalChannels) != len(want) {
		t.Fatalf("closed channels = %v, want %v", backendStub.closeLogicalChannels, want)
	}
	for i := range want {
		if backendStub.closeLogicalChannels[i] != want[i] {
			t.Fatalf("closed channels = %v, want %v", backendStub.closeLogicalChannels, want)
		}
	}
}

func TestCleanupWorkerStartupSIMAuthLogicalChannelsATFallbackContinuesAfterCloseFailure(t *testing.T) {
	backendStub := &workerStatusBackendStub{
		mode: backend.BackendAT,
		closeLogicalChannelErrs: map[int]error{
			2: fmt.Errorf("not open"),
		},
	}
	worker := &Worker{
		ID:      "dev-at",
		Config:  config.DeviceConfig{ID: "dev-at", DeviceBackend: backend.BackendAT},
		Backend: backendStub,
	}

	cleanupWorkerStartupSIMAuthLogicalChannels(worker)

	want := []int{1, 2, 3, 4}
	if len(backendStub.closeLogicalChannels) != len(want) {
		t.Fatalf("closed channels = %v, want %v", backendStub.closeLogicalChannels, want)
	}
	for i := range want {
		if backendStub.closeLogicalChannels[i] != want[i] {
			t.Fatalf("closed channels = %v, want %v", backendStub.closeLogicalChannels, want)
		}
	}
}

func TestNewWorkerBackendStrictDoesNotFallbackFromQMIToAT(t *testing.T) {
	be, err := newWorkerBackendStrict("dev-qmi", backend.BackendQMI, "/dev/cdc-wdm0", nil, nil, nil)
	if err == nil {
		t.Fatal("newWorkerBackendStrict() error = nil, want QMI backend init error")
	}
	if be != nil {
		t.Fatalf("newWorkerBackendStrict() backend = %#v, want nil", be)
	}
	if strings.Contains(err.Error(), "fallback") {
		t.Fatalf("newWorkerBackendStrict() error = %q, must not describe AT fallback", err.Error())
	}
}

func TestConfiguredDevicesNeedCompatibleATDiscoverySkipsCompleteQMI(t *testing.T) {
	devices := []config.DeviceConfig{
		{
			ID:            "dev-qmi",
			DeviceBackend: backend.BackendQMI,
			ModemIMEI:     "867123456789012",
			ControlDevice: "/dev/cdc-wdm0",
			Interface:     "wwan0",
			ATPort:        "/dev/ttyUSB2",
		},
	}

	if configuredDevicesNeedCompatibleATDiscovery(devices) {
		t.Fatal("configuredDevicesNeedCompatibleATDiscovery() = true for complete QMI attachment, want false")
	}
}

func TestConfiguredDevicesNeedCompatibleATDiscoveryForATMissingPort(t *testing.T) {
	devices := []config.DeviceConfig{
		{
			ID:            "dev-at",
			DeviceBackend: backend.BackendAT,
			ModemIMEI:     "867123456789012",
		},
	}

	if !configuredDevicesNeedCompatibleATDiscovery(devices) {
		t.Fatal("configuredDevicesNeedCompatibleATDiscovery() = false for AT config missing port, want true")
	}
}

func TestRequiresQMICoreForQMIESIMTransport(t *testing.T) {
	cfg := config.DeviceConfig{
		DeviceBackend: "at",
		ESIMTransport: config.ESIMTransportQMI,
	}

	if !requiresQMICore(cfg) {
		t.Fatal("expected qmi esim transport to require QMI core")
	}
}

func TestRequiresQMICoreWhenLegacyManagedNetworkCapabilityExists(t *testing.T) {
	cfg := config.DeviceConfig{
		ControlDevice: "/dev/cdc-wdm0",
		Interface:     "wwan0",
	}

	if !requiresQMICore(cfg) {
		t.Fatal("expected legacy managed QMI network capability to require QMI core")
	}
}

func TestNewVoWiFiModemInterfacePrefersQMIBackendOverAvailableATModem(t *testing.T) {
	backendStub := &workerStatusBackendStub{mode: backend.BackendQMI}
	worker := &Worker{
		ID:      "wwan0",
		Modem:   &modem.Manager{},
		Backend: backendStub,
	}

	got, err := newVoWiFiModemInterface(worker, "wwan0")
	if err != nil {
		t.Fatalf("newVoWiFiModemInterface() error = %v", err)
	}

	adapter, ok := got.(*qmiModemAdapter)
	if !ok {
		t.Fatalf("newVoWiFiModemInterface() = %T, want *qmiModemAdapter", got)
	}
	if adapter.backend != backendStub {
		t.Fatal("qmi adapter did not wrap worker backend")
	}
}

func TestBuildVoWiFiRuntimeModemDelegatesToAdapterSelection(t *testing.T) {
	backendStub := &workerStatusBackendStub{mode: backend.BackendQMI}
	worker := &Worker{
		ID:      "wwan0",
		Modem:   &modem.Manager{},
		Backend: backendStub,
	}

	got, err := BuildVoWiFiRuntimeModem(worker, "wwan0")
	if err != nil {
		t.Fatalf("BuildVoWiFiRuntimeModem() error = %v", err)
	}
	if _, ok := got.(*qmiModemAdapter); !ok {
		t.Fatalf("BuildVoWiFiRuntimeModem() = %T, want *qmiModemAdapter", got)
	}
}

func TestQMIModemAdapterOpenLogicalChannelUsesBackendSIMAuth(t *testing.T) {
	backendStub := &workerStatusBackendStub{mode: backend.BackendQMI}
	adapter := newQMIModemAdapter("dev-qmi", backendStub)

	ch, err := adapter.OpenLogicalChannel("A0000000871002")
	if err != nil {
		t.Fatalf("OpenLogicalChannel() error = %v", err)
	}
	if ch != 2 {
		t.Fatalf("OpenLogicalChannel() = %d, want 2", ch)
	}
	if backendStub.openLogicalChannelCalls != 1 {
		t.Fatalf("openLogicalChannelCalls = %d, want 1", backendStub.openLogicalChannelCalls)
	}

	if err := adapter.CloseLogicalChannel(2); err != nil {
		t.Fatalf("CloseLogicalChannel() error = %v", err)
	}
	if backendStub.closeLogicalChannelCalls != 1 {
		t.Fatalf("closeLogicalChannelCalls = %d, want 1", backendStub.closeLogicalChannelCalls)
	}
}

func TestQMIModemAdapterResolvesSIMAuthAIDThroughBackend(t *testing.T) {
	backendStub := &workerStatusBackendStub{
		mode:                  backend.BackendQMI,
		resolvedSIMAuthAID:    "A0000000871002FF49FF0189",
		resolvedSIMAuthSource: "qmi_card_status",
	}
	adapter := newQMIModemAdapter("dev-qmi", backendStub)

	aid, source, err := adapter.ResolveLogicalChannelAID("usim", "A0000000871002")
	if err != nil {
		t.Fatalf("ResolveLogicalChannelAID() error = %v", err)
	}
	if aid != "A0000000871002FF49FF0189" {
		t.Fatalf("resolved aid = %s, want full USIM AID", aid)
	}
	if source != "qmi_card_status" {
		t.Fatalf("source = %s, want qmi_card_status", source)
	}
}

func TestNewVoWiFiModemInterfaceUsesATModemForATBackend(t *testing.T) {
	worker := &Worker{
		ID:      "ttyUSB0",
		Modem:   &modem.Manager{},
		Backend: &workerStatusBackendStub{mode: backend.BackendAT},
	}

	got, err := newVoWiFiModemInterface(worker, "ttyUSB0")
	if err != nil {
		t.Fatalf("newVoWiFiModemInterface() error = %v", err)
	}
	if _, ok := got.(*modemAdapter); !ok {
		t.Fatalf("newVoWiFiModemInterface() = %T, want *modemAdapter", got)
	}
}

func TestRequiresQMICoreFalseForPureAT(t *testing.T) {
	cfg := config.DeviceConfig{
		DeviceBackend: "at",
		ESIMTransport: config.ESIMTransportAT,
	}

	if requiresQMICore(cfg) {
		t.Fatal("expected pure AT config to skip QMI core")
	}
}

func TestResolveDiscoveredCompatibleModemRequiresVerifiedATPortForIMEI(t *testing.T) {
	orig := probeIMEICachedFn
	defer func() { probeIMEICachedFn = orig }()

	probeIMEICachedFn = func(atPort string, timeout time.Duration) (string, error) {
		return "", fmt.Errorf("no imei")
	}

	dev, imei := resolveDiscoveredCompatibleModem(CompatibleModem{
		ATPorts: []string{"/dev/ttyUSB6", "/dev/ttyUSB7"},
		ATPort:  "/dev/ttyUSB6",
		IMEI:    "867123456789012",
	}, 50*time.Millisecond)

	if dev.ATPort != "" {
		t.Fatalf("resolved ATPort=%q want empty", dev.ATPort)
	}
	if imei != "" {
		t.Fatalf("resolved IMEI=%q want empty", imei)
	}
}

func TestPoolConfirmSIMRemovedStopsVoWiFi(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	backend := &workerStatusBackendStub{simInserted: false}
	w := &Worker{ID: "dev-1", Backend: backend}
	p.workers["dev-1"] = w
	p.voWiFiRuntimeStore().SetInstance("dev-1", &runtimehost.Instance{})
	if !p.voWiFiHost().BeginDesiredRecover("dev-1", time.Now().Add(-time.Minute)) {
		t.Fatal("expected desired recover state setup to begin")
	}

	var got vowifihost.LifecycleCommand
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = cmd
		return nil
	}

	p.confirmSIMRemovedAndStopVoWiFi("dev-1", "test")

	if got.Kind != vowifihost.LifecycleCommandDisable {
		t.Fatalf("expected disable command, got %v", got.Kind)
	}
	if got.Reason != "sim_removed" {
		t.Fatalf("expected sim_removed reason, got %q", got.Reason)
	}
	if p.voWiFiHost().HasDesiredRecoverState("dev-1") {
		t.Fatal("SIM 拔出后应清除 VoWiFi 目标态恢复状态")
	}
}

func TestWorkerProjectDeviceStatusIncludesSIMMetadataAndRadio(t *testing.T) {
	serviceTable := &backend.SIMServiceTable{Kind: "UST", RawHex: "05", EnabledServices: []int{1, 3}}
	worker := &Worker{}
	worker.state.Identity.NativeSPN = "CMCC"
	worker.state.Identity.NativeMCC = "460"
	worker.state.Identity.NativeMNC = "00"
	worker.state.Identity.GID1 = "01"
	worker.state.Identity.GID2 = "02"
	worker.state.Identity.PNN = []backend.PNNRecord{{Record: 1, FullName: "China Mobile", RawHex: "430C"}}
	worker.state.Identity.OPL = []backend.OPLRecord{{Record: 1, PLMN: "46000", LACStart: 1, LACEnd: 2, PNNRecord: 1}}
	worker.state.Identity.ServiceTable = serviceTable
	worker.state.Runtime.SignalSINR = 17
	worker.state.Runtime.NR5GSignalSINR = 23
	worker.state.Runtime.RadioBand = "LTE BAND 8"
	worker.state.Runtime.RadioChannel = 3740

	status := worker.ProjectDeviceStatus()
	if status.NativeSPN != "CMCC" || status.NativeMCC != "460" || status.NativeMNC != "00" || status.GID1 != "01" || status.GID2 != "02" {
		t.Fatalf("identity fields not projected: %+v", status)
	}
	if len(status.PNN) != 1 || status.PNN[0].FullName != "China Mobile" || len(status.OPL) != 1 || status.OPL[0].PLMN != "46000" {
		t.Fatalf("SIM records not projected: %+v %+v", status.PNN, status.OPL)
	}
	if status.SIMServiceTable == nil || status.SIMServiceTable.RawHex != "05" || len(status.SIMServiceTable.EnabledServices) != 2 {
		t.Fatalf("service table not projected: %+v", status.SIMServiceTable)
	}
	if status.SignalSINR != 17 || status.NR5GSignalSINR != 23 || status.RadioBand != "LTE BAND 8" || status.RadioChannel != 3740 {
		t.Fatalf("radio fields not projected: %+v", status)
	}
}

func TestMergeRuntimeStatePreservesRadioFieldsOnPartialRefresh(t *testing.T) {
	worker := &Worker{ID: "dev-at"}
	worker.state.Runtime.SignalDBM = -79
	worker.state.Runtime.SignalRSRP = -104
	worker.state.Runtime.SignalRSRQ = -8
	worker.state.Runtime.SignalSINR = 13
	worker.state.Runtime.NetworkDuplex = "FDD"
	worker.state.Runtime.NetworkMode = "LTE"
	worker.state.Runtime.RadioBand = "LTE BAND 8"
	worker.state.Runtime.RadioChannel = 3740
	worker.state.Runtime.RegStatus = 2
	worker.state.Runtime.RegStatusText = "搜索中"

	worker.mergeRuntimeStateLocked(modem.DeviceStatus{
		SignalDBM:     -79,
		RegStatus:     2,
		RegStatusText: "搜索中",
	}, true)

	status := worker.ProjectDeviceStatus()
	if status.SignalRSRP != -104 || status.SignalRSRQ != -8 || status.SignalSINR != 13 {
		t.Fatalf("partial refresh cleared LTE signal details: %+v", status)
	}
	if status.NetworkDuplex != "FDD" || status.NetworkMode != "LTE" || status.RadioBand != "LTE BAND 8" || status.RadioChannel != 3740 {
		t.Fatalf("partial refresh cleared radio access details: %+v", status)
	}
}

func TestWorkerGetDeviceStatusIncludesOperatingModeForATBackend(t *testing.T) {
	worker := &Worker{
		Backend: &workerStatusBackendStub{
			mode:   backend.BackendAT,
			opMode: backend.ModeRFOff,
		},
	}

	status := worker.GetDeviceStatus()
	if status.OperatingMode == nil {
		t.Fatal("expected operating mode to be populated")
	}
	if *status.OperatingMode != int(backend.ModeRFOff) {
		t.Fatalf("operating_mode=%d want=%d", *status.OperatingMode, int(backend.ModeRFOff))
	}
}

func TestWorkerRefreshRuntimeStoresPSAttached(t *testing.T) {
	worker := &Worker{
		ID: "dev-qmi",
		Backend: &workerStatusBackendStub{
			mode: backend.BackendQMI,
			serving: &backend.ServingSystem{
				RegStatus:     1,
				RegStatusText: "已注册(本地)",
				PSAttached:    true,
				NetworkMode:   "LTE",
			},
		},
	}

	if err := worker.RefreshRuntime(context.Background(), "test"); err != nil {
		t.Fatalf("RefreshRuntime() error = %v", err)
	}
	status := worker.ProjectDeviceStatus()
	if !status.PSAttached {
		t.Fatal("PSAttached=false, want true")
	}
}

func TestWorkerMarkHealthRecoveryWindow(t *testing.T) {
	worker := &Worker{}
	worker.markHealthRecoveryWindow(2 * time.Second)

	if got := worker.healthRecoveryRemaining(time.Now()); got <= 0 {
		t.Fatalf("healthRecoveryRemaining() = %v, want > 0", got)
	}
}

func TestSuppressQMIUnhealthyEvictionWhenESIMSwitching(t *testing.T) {
	pool := NewPool(&config.Config{})
	worker := &Worker{
		ID: "dev1",
		Config: config.DeviceConfig{
			ID:            "dev1",
			DeviceBackend: backend.BackendQMI,
			ControlDevice: "/dev/cdc-wdm0",
		},
		Backend: &workerStatusBackendStub{mode: backend.BackendQMI, opMode: backend.ModeOnline},
	}
	pool.workers["dev1"] = worker

	pool.switchMu.Lock()
	pool.switchingDevices["dev1"] = true
	pool.switchMu.Unlock()

	suppressed, reason := pool.suppressQMIUnhealthyEviction(worker)
	if !suppressed {
		t.Fatal("expected suppression while eSIM switching")
	}
	if !strings.Contains(reason, "esim_switching") {
		t.Fatalf("reason=%q want contains esim_switching", reason)
	}
}

func TestSuppressQMIUnhealthyEvictionWhenRFOff(t *testing.T) {
	pool := NewPool(&config.Config{})
	worker := &Worker{
		ID: "dev1",
		Config: config.DeviceConfig{
			ID:            "dev1",
			DeviceBackend: backend.BackendQMI,
			ControlDevice: "/dev/cdc-wdm0",
		},
		Backend: &workerStatusBackendStub{mode: backend.BackendQMI, opMode: backend.ModeRFOff},
	}

	suppressed, reason := pool.suppressQMIUnhealthyEviction(worker)
	if !suppressed {
		t.Fatal("expected suppression when modem is RF off")
	}
	if !strings.Contains(reason, "operating_mode") {
		t.Fatalf("reason=%q want contains operating_mode", reason)
	}
}

func TestSuppressQMIUnhealthyEvictionWhenRegistrationReconcileInFlight(t *testing.T) {
	pool := NewPool(&config.Config{})
	worker := &Worker{
		ID: "dev1",
		Config: config.DeviceConfig{
			ID:            "dev1",
			DeviceBackend: backend.BackendQMI,
			ControlDevice: "/dev/cdc-wdm0",
		},
		// 故意不设置 Backend：若该项检查未在 Backend 探测之前生效，
		// 后续的 GetOperatingMode 调用会 panic，从而暴露顺序错误。
	}
	worker.qmiRegistrationMu.Lock()
	worker.qmiRegistrationInFlight = true
	worker.qmiRegistrationMu.Unlock()

	suppressed, reason := pool.suppressQMIUnhealthyEviction(worker)
	if !suppressed {
		t.Fatal("expected suppression while QMI 后台驻网协调 in flight")
	}
	if !strings.Contains(reason, "registration_reconcile") {
		t.Fatalf("reason=%q want contains registration_reconcile", reason)
	}
}

func TestBuildVoWiFiStartProfileUsesLiveIMSIOnly(t *testing.T) {
	p := NewPool(&config.Config{})
	w := &Worker{
		ID:      "dev1",
		Backend: &workerStatusBackendStub{mode: backend.BackendQMI},
	}
	w.cacheMu.Lock()
	w.state.Identity.IMSI = "460001234567890"
	w.state.Identity.IMEI = "861234567890123"
	w.cacheMu.Unlock()

	_, err := p.buildVoWiFiStartProfile(w, "trace-live")
	if err == nil || !strings.Contains(err.Error(), "实时 IMSI 为空") {
		t.Fatalf("buildVoWiFiStartProfile() err=%v, want 实时 IMSI 为空", err)
	}
}

type vowifiLiveIdentityBackendStub struct {
	workerSMSCBackendStub
	liveIMSI string
}

func (s *vowifiLiveIdentityBackendStub) GetIMSILive(ctx context.Context) (string, error) {
	return s.liveIMSI, nil
}

func (s *vowifiLiveIdentityBackendStub) GetICCIDLive(ctx context.Context) (string, error) {
	return "", nil
}

func TestBuildVoWiFiStartProfileUsesLiveIMSIAndBackendHomeMCCMNC(t *testing.T) {
	p := NewPool(&config.Config{})
	b := &vowifiLiveIdentityBackendStub{
		workerSMSCBackendStub: workerSMSCBackendStub{
			workerStatusBackendStub: workerStatusBackendStub{mode: backend.BackendQMI},
			seq:                     []smscResult{{value: "+8613800250500"}},
		},
		liveIMSI: "530240209434655",
	}
	b.nativeMCC = "530"
	b.nativeMNC = "24"
	w := &Worker{ID: "dev1", Backend: b}
	w.cacheMu.Lock()
	w.state.Identity.IMSI = "460001234567890"
	w.state.Identity.IMEI = "861234567890123"
	w.cacheMu.Unlock()

	profile, err := p.buildVoWiFiStartProfile(w, "trace-live-ok")
	if err != nil {
		t.Fatalf("buildVoWiFiStartProfile() error=%v", err)
	}
	if profile.IMSI != "530240209434655" {
		t.Fatalf("profile.IMSI=%q want live IMSI", profile.IMSI)
	}
	norm := identity.NormalizeProfile(identity.Profile{MCC: profile.MCC, MNC: profile.MNC})
	if norm.MCC != "530" || norm.MNC != "24" {
		t.Fatalf("profile MCC/MNC=%s/%s want 530/24", norm.MCC, norm.MNC)
	}
}

func TestBuildVoWiFiStartProfileUsesLiveHomeMCCMNCInsteadOfStaleCache(t *testing.T) {
	p := NewPool(&config.Config{})
	b := &vowifiLiveIdentityBackendStub{
		workerSMSCBackendStub: workerSMSCBackendStub{
			workerStatusBackendStub: workerStatusBackendStub{
				mode:      backend.BackendQMI,
				nativeMCC: "234",
				nativeMNC: "33",
			},
			seq: []smscResult{{value: "+8613800250500"}},
		},
		liveIMSI: "234336575868434",
	}
	w := &Worker{ID: "dev1", Backend: b}
	w.cacheMu.Lock()
	w.state.Identity.IMSI = "234336575868434"
	w.state.Identity.IMEI = "861234567890123"
	w.state.Identity.NativeMCC = "234"
	w.state.Identity.NativeMNC = "030"
	w.cacheMu.Unlock()

	profile, err := p.buildVoWiFiStartProfile(w, "trace-home-plmn")
	if err != nil {
		t.Fatalf("buildVoWiFiStartProfile() error=%v", err)
	}
	if profile.IMSI != "234336575868434" {
		t.Fatalf("profile.IMSI=%q want live IMSI", profile.IMSI)
	}
	if profile.MCC != "234" || profile.MNC != "33" {
		t.Fatalf("profile MCC/MNC=%s/%s want home 234/33", profile.MCC, profile.MNC)
	}
}

func TestBuildVoWiFiStartProfileRequiresCachedHomeMCCMNC(t *testing.T) {
	p := NewPool(&config.Config{})
	b := &vowifiLiveIdentityBackendStub{
		workerSMSCBackendStub: workerSMSCBackendStub{
			workerStatusBackendStub: workerStatusBackendStub{
				mode: backend.BackendQMI,
			},
			seq: []smscResult{{value: "+8613800250500"}},
		},
		liveIMSI: "234336575868434",
	}
	w := &Worker{ID: "dev1", Backend: b}
	w.cacheMu.Lock()
	w.state.Identity.IMSI = "234336575868434"
	w.state.Identity.IMEI = "861234567890123"
	w.cacheMu.Unlock()

	if _, err := p.buildVoWiFiStartProfile(w, "trace-missing-home-plmn"); err == nil {
		t.Fatal("buildVoWiFiStartProfile() err=nil, want missing home MCC/MNC error")
	}
}

func TestRefreshIdentityLiveCachesHomeMCCMNCThroughSIMMetadata(t *testing.T) {
	b := &vowifiLiveIdentityBackendStub{
		workerSMSCBackendStub: workerSMSCBackendStub{
			workerStatusBackendStub: workerStatusBackendStub{
				mode: backend.BackendQMI,
				simMetadata: &backend.SIMMetadata{
					NativeMCC: "234",
					NativeMNC: "33",
				},
			},
		},
		liveIMSI: "234336575868434",
	}
	w := &Worker{ID: "dev1", Backend: b}
	w.cacheMu.Lock()
	w.state.Identity.ICCID = "old"
	w.state.Identity.IMSI = "old-imsi"
	w.state.Identity.NativeMCC = "234"
	w.state.Identity.NativeMNC = "030"
	w.cacheMu.Unlock()

	if _, err := w.refreshIdentityLive(context.Background(), "test_home_mcc_mnc"); err != nil {
		t.Fatalf("refreshIdentityLive() error=%v", err)
	}

	status := w.ProjectDeviceStatus()
	if status.NativeMCC != "234" || status.NativeMNC != "33" {
		t.Fatalf("cached SIM home MCC/MNC = %s/%s, want 234/33", status.NativeMCC, status.NativeMNC)
	}
}

func TestRefreshIdentityLiveCachesHomeMCCMNCThroughBackendWhenMetadataEmpty(t *testing.T) {
	b := &vowifiLiveIdentityBackendStub{
		workerSMSCBackendStub: workerSMSCBackendStub{
			workerStatusBackendStub: workerStatusBackendStub{
				mode:      backend.BackendQMI,
				nativeMCC: "234",
				nativeMNC: "33",
			},
		},
		liveIMSI: "234336575868434",
	}
	w := &Worker{ID: "dev1", Backend: b}

	if _, err := w.refreshIdentityLive(context.Background(), "test_home_mcc_mnc_backend"); err != nil {
		t.Fatalf("refreshIdentityLive() error=%v", err)
	}

	status := w.ProjectDeviceStatus()
	if status.NativeMCC != "234" || status.NativeMNC != "33" {
		t.Fatalf("cached SIM home MCC/MNC = %s/%s, want 234/33", status.NativeMCC, status.NativeMNC)
	}
}

func TestWorkerHealthFailureStreakReset(t *testing.T) {
	worker := &Worker{}

	if got := worker.recordHealthFailure(); got != 1 {
		t.Fatalf("recordHealthFailure() first=%d want=1", got)
	}
	if got := worker.recordHealthFailure(); got != 2 {
		t.Fatalf("recordHealthFailure() second=%d want=2", got)
	}
	worker.resetHealthFailureStreak()
	if got := worker.recordHealthFailure(); got != 1 {
		t.Fatalf("recordHealthFailure() after reset=%d want=1", got)
	}
}
