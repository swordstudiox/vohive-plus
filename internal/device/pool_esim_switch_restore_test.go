package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/cardpolicy"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/esim"
	qmicore "github.com/swordstudiox/vohive-plus/internal/qmi"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

type esimSwitchRestoreBackendStub struct {
	mode                string
	getMode             backend.OperatingMode
	setCalls            []backend.OperatingMode
	liveICCID           string
	liveIMSI            string
	liveSPN             string
	liveSPNErr          error
	uimReadiness        []qmimanager.UIMReadiness
	uimReadinessCalls   int
	simMetadata         *backend.SIMMetadata
	simMetadataErr      error
	metadataStarted     chan struct{}
	metadataRelease     <-chan struct{}
	closeChannels       []int
	openChannelID       int
	openChannelAIDs     []string
	openChannelErrs     []error
	resolvedSIMAuthAID  string
	resolvedSIMAuthSrc  string
	resolvedSIMAuthErr  error
	resolvedSIMAuthErrs []error
	resolveSIMAuthApps  []string
	resolveStarted      chan<- struct{}
	resolveRelease      <-chan struct{}
	openChannelStarted  chan<- struct{}
	openChannelRelease  <-chan struct{}
	setModeHook         func(backend.OperatingMode)
}

func setPrivateFieldSwitchRestore(t *testing.T, target any, fieldName string, value any) {
	t.Helper()
	field := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func setPrivateMapFieldSwitchRestore(t *testing.T, target any, fieldName string, key any, value func(reflect.Type) reflect.Value) {
	t.Helper()
	field := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	m := reflect.MakeMap(field.Type())
	m.SetMapIndex(reflect.ValueOf(key), value(field.Type().Elem()))
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(m)
}

func privateMapLenSwitchRestore(t *testing.T, target any, fieldName string) int {
	t.Helper()
	field := reflect.ValueOf(target).Elem().FieldByName(fieldName)
	return field.Len()
}

func withFastPostSwitchSIMAuthRecovery(t *testing.T) {
	t.Helper()
	original := append([]time.Duration(nil), postSwitchSIMAuthRecoveryDelays...)
	postSwitchSIMAuthRecoveryDelays = []time.Duration{0, 0}
	t.Cleanup(func() {
		postSwitchSIMAuthRecoveryDelays = original
	})
}

func withDeviceEventRecoverWakeDelay(t *testing.T, delay time.Duration) {
	t.Helper()
	original := deviceEventRecoverWakeDelay
	deviceEventRecoverWakeDelay = delay
	t.Cleanup(func() {
		deviceEventRecoverWakeDelay = original
	})
}

func repeatedSIMAuthOpenErrors(count int) []error {
	errs := make([]error, 0, count)
	for i := 0; i < count; i++ {
		errs = append(errs, fmt.Errorf("sim_auth_open_not_ready_%d", i+1))
	}
	return errs
}

func withFastPostSwitchIdentityPolling(t *testing.T) {
	t.Helper()
	originalTimeout := postSwitchIdentityPollTimeout
	originalInterval := postSwitchIdentityPollInterval
	postSwitchIdentityPollTimeout = 20 * time.Millisecond
	postSwitchIdentityPollInterval = time.Millisecond
	t.Cleanup(func() {
		postSwitchIdentityPollTimeout = originalTimeout
		postSwitchIdentityPollInterval = originalInterval
	})
}

func withImmediatePostSwitchIdentityRetries(t *testing.T) {
	t.Helper()
	original := append([]time.Duration(nil), postSwitchIdentityRetryDelays...)
	postSwitchIdentityRetryDelays = []time.Duration{0}
	t.Cleanup(func() {
		postSwitchIdentityRetryDelays = original
	})
}

func (s *esimSwitchRestoreBackendStub) GetIMEI(ctx context.Context) (string, error) { return "", nil }
func (s *esimSwitchRestoreBackendStub) GetIMSI(ctx context.Context) (string, error) {
	if s.liveIMSI != "" {
		return s.liveIMSI, nil
	}
	return "", nil
}
func (s *esimSwitchRestoreBackendStub) GetIMSILive(ctx context.Context) (string, error) {
	return s.GetIMSI(ctx)
}
func (s *esimSwitchRestoreBackendStub) GetICCID(ctx context.Context) (string, error) {
	if s.liveICCID != "" {
		return s.liveICCID, nil
	}
	return "", nil
}
func (s *esimSwitchRestoreBackendStub) GetICCIDLive(ctx context.Context) (string, error) {
	return s.GetICCID(ctx)
}
func (s *esimSwitchRestoreBackendStub) GetUIMReadiness(ctx context.Context) (qmimanager.UIMReadiness, error) {
	if len(s.uimReadiness) > 0 {
		idx := s.uimReadinessCalls
		if idx >= len(s.uimReadiness) {
			idx = len(s.uimReadiness) - 1
		}
		s.uimReadinessCalls++
		out := s.uimReadiness[idx]
		return out, out.Err
	}
	return qmimanager.UIMReadiness{
		TransportReady: true,
		ControlReady:   true,
		UIMReady:       true,
		CardPresent:    true,
		SIMStatus:      qmi.SIMReady,
		ActiveSlot:     1,
		SlotKnown:      true,
		SlotSource:     "test_default",
		ICCID:          s.liveICCID,
		IMSI:           s.liveIMSI,
		Reason:         qmimanager.UIMReadinessReady,
	}, nil
}
func (s *esimSwitchRestoreBackendStub) GetMSISDN(ctx context.Context) (string, error) {
	return "", nil
}
func (s *esimSwitchRestoreBackendStub) GetRevision(ctx context.Context) (string, error) {
	return "", nil
}
func (s *esimSwitchRestoreBackendStub) GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error) {
	return nil, nil
}
func (s *esimSwitchRestoreBackendStub) GetServingSystem(ctx context.Context) (*backend.ServingSystem, error) {
	return nil, nil
}
func (s *esimSwitchRestoreBackendStub) IsSimInserted(ctx context.Context) (bool, error) {
	return true, nil
}
func (s *esimSwitchRestoreBackendStub) GetNativeMCCMNC(ctx context.Context) (string, string, error) {
	return "", "", nil
}

func (s *esimSwitchRestoreBackendStub) GetNativeSPN(ctx context.Context) (string, error) {
	return s.liveSPN, s.liveSPNErr
}
func (s *esimSwitchRestoreBackendStub) GetNativeSPNLive(ctx context.Context) (string, error) {
	return s.GetNativeSPN(ctx)
}
func (s *esimSwitchRestoreBackendStub) GetSIMMetadata(ctx context.Context) (*backend.SIMMetadata, error) {
	if s.metadataStarted != nil {
		close(s.metadataStarted)
		s.metadataStarted = nil
	}
	if s.metadataRelease != nil {
		<-s.metadataRelease
	}
	return s.simMetadata, s.simMetadataErr
}
func (s *esimSwitchRestoreBackendStub) GetSIMMetadataLive(ctx context.Context) (*backend.SIMMetadata, error) {
	return s.GetSIMMetadata(ctx)
}
func (s *esimSwitchRestoreBackendStub) SendSMS(ctx context.Context, to, body string) error {
	return nil
}
func (s *esimSwitchRestoreBackendStub) ReadSMS(ctx context.Context, index int) (*backend.SMS, error) {
	return nil, nil
}
func (s *esimSwitchRestoreBackendStub) DeleteSMS(ctx context.Context, index int) error { return nil }
func (s *esimSwitchRestoreBackendStub) ListSMS(ctx context.Context) ([]backend.SMSSummary, error) {
	return nil, nil
}
func (s *esimSwitchRestoreBackendStub) DeleteAllSMS(ctx context.Context) error { return nil }
func (s *esimSwitchRestoreBackendStub) SetOperatingMode(ctx context.Context, mode backend.OperatingMode) error {
	s.setCalls = append(s.setCalls, mode)
	if s.setModeHook != nil {
		s.setModeHook(mode)
	}
	return nil
}
func (s *esimSwitchRestoreBackendStub) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	return s.getMode, nil
}
func (s *esimSwitchRestoreBackendStub) Reboot(ctx context.Context) error { return nil }
func (s *esimSwitchRestoreBackendStub) OpenLogicalChannel(ctx context.Context, aid string) (int, error) {
	s.openChannelAIDs = append(s.openChannelAIDs, aid)
	if s.openChannelStarted != nil {
		select {
		case s.openChannelStarted <- struct{}{}:
		default:
		}
	}
	if s.openChannelRelease != nil {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-s.openChannelRelease:
		}
	}
	if len(s.openChannelErrs) > 0 {
		err := s.openChannelErrs[0]
		s.openChannelErrs = s.openChannelErrs[1:]
		if err != nil {
			return 0, err
		}
	}
	if s.openChannelID != 0 {
		return s.openChannelID, nil
	}
	return 2, nil
}
func (s *esimSwitchRestoreBackendStub) ResolveSIMAuthAID(ctx context.Context, app string, fallbackAID string) (string, string, error) {
	s.resolveSIMAuthApps = append(s.resolveSIMAuthApps, app)
	if s.resolveStarted != nil {
		select {
		case s.resolveStarted <- struct{}{}:
		default:
		}
	}
	if s.resolveRelease != nil {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-s.resolveRelease:
		}
	}
	if len(s.resolvedSIMAuthErrs) > 0 {
		err := s.resolvedSIMAuthErrs[0]
		s.resolvedSIMAuthErrs = s.resolvedSIMAuthErrs[1:]
		if err != nil {
			return "", "", err
		}
	}
	if s.resolvedSIMAuthErr != nil {
		return "", "", s.resolvedSIMAuthErr
	}
	if s.resolvedSIMAuthAID != "" {
		return s.resolvedSIMAuthAID, s.resolvedSIMAuthSrc, nil
	}
	return "", "sim_auth_aid_not_ready", errors.New("sim_auth_aid_not_ready")
}
func (s *esimSwitchRestoreBackendStub) CloseLogicalChannel(ctx context.Context, channelID int) error {
	s.closeChannels = append(s.closeChannels, channelID)
	return nil
}
func (s *esimSwitchRestoreBackendStub) TransmitAPDU(ctx context.Context, channelID int, command string) (string, error) {
	return "", nil
}
func (s *esimSwitchRestoreBackendStub) Mode() string {
	if s.mode == "" {
		return backend.BackendQMI
	}
	return s.mode
}
func (s *esimSwitchRestoreBackendStub) Close() error { return nil }

func withSwitchSnapshot(p *Pool, deviceID string, snapshot esimSwitchContext) {
	p.switchMu.Lock()
	p.switchingDevices[deviceID] = true
	p.switchContexts[deviceID] = snapshot
	p.switchMu.Unlock()
}

func currentSwitchSnapshotSwitchRestore(t *testing.T, p *Pool, deviceID string) (esimSwitchContext, bool) {
	t.Helper()
	p.switchMu.Lock()
	defer p.switchMu.Unlock()
	snapshot, ok := p.switchContexts[deviceID]
	return snapshot, ok
}

func waitForModemRebootRecoverySwitchRestore(t *testing.T, p *Pool, deviceID string, want bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		p.mu.RLock()
		got := p.modemRebootRecovering[deviceID]
		p.mu.RUnlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("modemRebootRecovering[%q]=%v want %v", deviceID, got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStalePostSwitchHookDoesNotClearNewSwitch(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-1"
	first := p.beginESIMSwitch(deviceID, "")
	second := p.beginESIMSwitch(deviceID, "")

	p.handleESIMSwitchAfter(deviceID, first.SwitchToken)

	if !p.IsESIMSwitching(deviceID) {
		t.Fatal("expected stale post-switch hook to keep newer switch active")
	}
	got, ok := currentSwitchSnapshotSwitchRestore(t, p, deviceID)
	if !ok {
		t.Fatal("expected newer switch snapshot to remain")
	}
	if got.SwitchToken != second.SwitchToken {
		t.Fatalf("current switch token=%d want %d", got.SwitchToken, second.SwitchToken)
	}
}

func TestStaleSwitchFailedDoesNotClearNewSwitch(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-1"
	first := p.beginESIMSwitch(deviceID, "")
	second := p.beginESIMSwitch(deviceID, "")

	p.handleESIMSwitchFailed(deviceID, first.SwitchToken)

	if !p.IsESIMSwitching(deviceID) {
		t.Fatal("expected stale failed hook to keep newer switch active")
	}
	got, ok := currentSwitchSnapshotSwitchRestore(t, p, deviceID)
	if !ok {
		t.Fatal("expected newer switch snapshot to remain")
	}
	if got.SwitchToken != second.SwitchToken {
		t.Fatalf("current switch token=%d want %d", got.SwitchToken, second.SwitchToken)
	}
}

func TestESIMSwitchFailedCallbackDoesNotRouteFatalErrorToRecovery(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	deviceID := "dev-1"
	snapshot := p.beginESIMSwitch(deviceID, "")
	_, _, onFailed, _, _ := p.newESIMSwitchCallbacks(deviceID)

	onFailed(snapshot.SwitchToken, errors.New("QMI: read failed: EOF"))

	if p.IsESIMSwitching(deviceID) {
		t.Fatal("expected switching flag cleared after failed callback")
	}
	waitForModemRebootRecoverySwitchRestore(t, p, deviceID, false)
}

func TestSwitchPhaseIgnoresStaleToken(t *testing.T) {
	p := NewPool(&config.Config{})
	deviceID := "dev-1"
	first := p.beginESIMSwitch(deviceID, "")
	second := p.beginESIMSwitch(deviceID, "")

	if p.markESIMSwitchPhaseIfToken(deviceID, first.SwitchToken, esim.SwitchPhaseDone) {
		t.Fatal("expected stale switch phase update to be ignored")
	}
	got, ok := currentSwitchSnapshotSwitchRestore(t, p, deviceID)
	if !ok {
		t.Fatal("expected newer switch snapshot to remain")
	}
	if got.SwitchToken != second.SwitchToken {
		t.Fatalf("current switch token=%d want %d", got.SwitchToken, second.SwitchToken)
	}
	if got.Phase != esim.SwitchPhasePrepare {
		t.Fatalf("phase after stale update=%q want %q", got.Phase, esim.SwitchPhasePrepare)
	}

	if !p.markESIMSwitchPhaseIfToken(deviceID, second.SwitchToken, esim.SwitchPhaseAPDUSwitching) {
		t.Fatal("expected current switch phase update to be applied")
	}
	got, ok = currentSwitchSnapshotSwitchRestore(t, p, deviceID)
	if !ok {
		t.Fatal("expected current switch snapshot to remain")
	}
	if got.Phase != esim.SwitchPhaseAPDUSwitching {
		t.Fatalf("phase after current update=%q want %q", got.Phase, esim.SwitchPhaseAPDUSwitching)
	}
}

func TestHandleESIMSwitchBeforeSubmitsSwitchBeginThroughLifecycleController(t *testing.T) {
	p := NewPool(&config.Config{})

	var got []string
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	p.handleESIMSwitchBefore("dev-1", "")

	want := []string{"switch_begin:dev-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("submitted commands = %v, want %v", got, want)
	}
}

func TestHandleESIMSwitchBeforeRadioCycleReleasesRadioAfterSwitchBegin(t *testing.T) {
	p := NewPool(&config.Config{})
	var got []string
	be := &esimSwitchRestoreBackendStub{
		mode:    backend.BackendQMI,
		getMode: backend.ModeOnline,
		setModeHook: func(mode backend.OperatingMode) {
			got = append(got, fmt.Sprintf("set_mode:%d", mode))
		},
	}
	w := &Worker{
		ID: "dev-1",
		Config: config.DeviceConfig{
			ID: "dev-1",
			ESIMSwitch: config.ESIMSwitchConfig{
				RadioCycle: true,
			},
		},
		Backend: be,
	}
	p.workers["dev-1"] = w
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	p.handleESIMSwitchBefore("dev-1", "")

	want := []string{"switch_begin:dev-1", fmt.Sprintf("set_mode:%d", backend.ModeLowPower)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("operations=%v want %v", got, want)
	}
}

func TestHandleESIMSwitchAfterSubmitsSwitchEndThroughLifecycleController(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{
		mode:               backend.BackendQMI,
		getMode:            backend.ModeOnline,
		liveICCID:          "460001234567890",
		liveIMSI:           "460001234567890",
		resolvedSIMAuthAID: "A0000000871002FFFFFFFF8903020000",
		resolvedSIMAuthSrc: "qmi_card_status",
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{})
	p.voWiFiHost().MarkDesiredRecoverFailed("dev-1", time.Now(), errors.New("previous recover failed"))

	var got []string
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	p.handleESIMSwitchAfter("dev-1", 0)

	want := []string{"switch_end:dev-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("submitted commands = %v, want %v", got, want)
	}
	if p.voWiFiHost().HasDesiredRecoverState("dev-1") {
		t.Fatal("desired recover state should be cleared before switch restore")
	}
}

func TestHandleESIMSwitchAfterRadioCycleOnlineThenSnapshotRestore(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{
		mode:      backend.BackendQMI,
		getMode:   backend.ModeOnline,
		liveICCID: "460001234567890",
		liveIMSI:  "460001234567890",
	}
	w := &Worker{
		ID: "dev-1",
		Config: config.DeviceConfig{
			ID:             "dev-1",
			VoWiFiEnabled:  false,
			NetworkEnabled: true,
			ESIMSwitch: config.ESIMSwitchConfig{
				RadioCycle: true,
			},
		},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{
		FlightModeBefore: true,
	})

	p.handleESIMSwitchAfter("dev-1", 0)

	want := []backend.OperatingMode{backend.ModeOnline, backend.ModeRFOff}
	if !reflect.DeepEqual(be.setCalls, want) {
		t.Fatalf("setCalls=%v want %v", be.setCalls, want)
	}
}

func TestHandleESIMSwitchAfterRecoversSIMAuthBeforeVoWiFiRestore(t *testing.T) {
	withFastPostSwitchSIMAuthRecovery(t)
	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &esimSwitchRestoreBackendStub{
		mode:               backend.BackendQMI,
		getMode:            backend.ModeOnline,
		liveICCID:          "460001234567890",
		liveIMSI:           "460001234567890",
		resolvedSIMAuthAID: "A0000000871002FFFFFFFF8903020000",
		resolvedSIMAuthSrc: "qmi_card_status",
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{})

	var got []string
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	p.handleESIMSwitchAfter("dev-1", 0)

	wantCommands := []string{"switch_end:dev-1"}
	if !reflect.DeepEqual(got, wantCommands) {
		t.Fatalf("submitted commands = %v, want %v", got, wantCommands)
	}
}

func TestDefaultESIMPostSwitchMinDelayIsOneSecond(t *testing.T) {
	if defaultESIMPostSwitchMinDelay != time.Second {
		t.Fatalf("defaultESIMPostSwitchMinDelay=%s want 1s", defaultESIMPostSwitchMinDelay)
	}
}

func TestWakeDesiredVoWiFiRecoverFromDeviceEventCoalescesBurst(t *testing.T) {
	withDeviceEventRecoverWakeDelay(t, 10*time.Millisecond)
	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &esimSwitchRestoreBackendStub{
		mode:      backend.BackendQMI,
		getMode:   backend.ModeOnline,
		liveICCID: "460001234567890",
		liveIMSI:  "460001234567890",
	}
	p.SetPolicyResolver(&stubPolicyResolver{
		pol: cardpolicy.Policy{ICCID: "460001234567890", VoWiFiEnabled: true},
	})
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	w.state.Identity.ICCID = "460001234567890"
	w.state.Identity.IMSI = "460001234567890"
	p.workers["dev-1"] = w
	commands := make(chan vowifihost.LifecycleCommand, 4)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.wakeDesiredVoWiFiRecoverFromDeviceEvent("dev-1", "post_switch_uim_slot_status")
	p.wakeDesiredVoWiFiRecoverFromDeviceEvent("dev-1", "post_switch_qmi_sim_status")
	p.wakeDesiredVoWiFiRecoverFromDeviceEvent("dev-1", "post_switch_modem_ready")

	select {
	case cmd := <-commands:
		if cmd.Kind != vowifihost.LifecycleCommandRecover {
			t.Fatalf("kind=%s want recover", cmd.Kind.String())
		}
		if cmd.Reason != "post_switch_event_wakeup" {
			t.Fatalf("reason=%q want post_switch_event_wakeup", cmd.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced recover was not submitted")
	}

	select {
	case cmd := <-commands:
		t.Fatalf("unexpected duplicate recover command: %s", cmd.Kind.String())
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPrewarmPostSwitchSIMAuthRetriesUntilReadyWithinExpandedWindow(t *testing.T) {
	original := append([]time.Duration(nil), postSwitchSIMAuthRecoveryDelays...)
	postSwitchSIMAuthRecoveryDelays = []time.Duration{0, 0, 0, 0, 0}
	t.Cleanup(func() { postSwitchSIMAuthRecoveryDelays = original })

	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{
		mode:               backend.BackendQMI,
		getMode:            backend.ModeOnline,
		liveICCID:          "460001234567890",
		liveIMSI:           "460001234567890",
		resolvedSIMAuthAID: "A0000000871002FFFFFFFF8903020000",
		resolvedSIMAuthSrc: "qmi_card_status",
		resolvedSIMAuthErrs: []error{
			errors.New("sim_auth_aid_not_ready_1"),
			errors.New("sim_auth_aid_not_ready_2"),
			errors.New("sim_auth_aid_not_ready_3"),
			errors.New("sim_auth_aid_not_ready_4"),
			errors.New("sim_auth_aid_not_ready_5"),
			nil,
		},
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{})

	var got []string
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	result := p.prewarmPostSwitchSIMAuth("dev-1", w)

	if !result.Ready {
		t.Fatal("prewarm result should be ready after expanded retry window")
	}
	if len(be.resolveSIMAuthApps) != 6 {
		t.Fatalf("resolveSIMAuthApps len=%d want 6", len(be.resolveSIMAuthApps))
	}
	for _, app := range be.resolveSIMAuthApps {
		if app != "usim" {
			t.Fatalf("resolved app=%q want usim", app)
		}
	}
	if len(be.openChannelAIDs) != 0 {
		t.Fatalf("openChannelAIDs=%v want no open-channel validation", be.openChannelAIDs)
	}
	if len(got) != 0 {
		t.Fatalf("prewarm should not submit lifecycle commands, got %v", got)
	}
}

func TestPrewarmPostSwitchSIMAuthUsesResolvedUSIMAID(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{
		mode:               backend.BackendQMI,
		getMode:            backend.ModeOnline,
		liveICCID:          "460001234567890",
		liveIMSI:           "460001234567890",
		resolvedSIMAuthAID: "A0000000871002FFFFFFFF8903020000",
		resolvedSIMAuthSrc: "qmi_card_status",
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{})

	result := p.prewarmPostSwitchSIMAuth("dev-1", w)

	if !result.Ready {
		t.Fatalf("prewarm result ready=%v err=%v", result.Ready, result.Err)
	}
	if len(be.resolveSIMAuthApps) != 1 || be.resolveSIMAuthApps[0] != "usim" {
		t.Fatalf("resolveSIMAuthApps=%v want [usim]", be.resolveSIMAuthApps)
	}
	if len(be.openChannelAIDs) != 0 {
		t.Fatalf("openChannelAIDs=%v want no open-channel validation", be.openChannelAIDs)
	}
}

func TestHandleESIMSwitchAfterWaitsForSIMAuthReadyBeforeSwitchEnd(t *testing.T) {
	withFastPostSwitchSIMAuthRecovery(t)

	p := NewPool(&config.Config{})
	defer p.cancel()
	resolveStarted := make(chan struct{}, 1)
	resolveRelease := make(chan struct{})
	be := &esimSwitchRestoreBackendStub{
		mode:               backend.BackendAT,
		getMode:            backend.ModeOnline,
		liveICCID:          "460001234567890",
		liveIMSI:           "460001234567890",
		resolvedSIMAuthAID: "A0000000871002FFFFFFFF8903020000",
		resolvedSIMAuthSrc: "at_ef_dir",
		resolveStarted:     resolveStarted,
		resolveRelease:     resolveRelease,
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{FlightModeBefore: false})

	var got []string
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		commands <- cmd
		p.voWiFiHost().BeginStart(cmd.DeviceID)
		return nil
	}

	done := make(chan struct{})
	go func() {
		p.handleESIMSwitchAfter("dev-1", 0)
		close(done)
	}()

	select {
	case <-resolveStarted:
	case <-time.After(time.Second):
		t.Fatal("SIMAuth AID gate did not start before VoWiFi restore")
	}

	select {
	case cmd := <-commands:
		t.Fatalf("unexpected lifecycle command before SIMAuth ready: %s", cmd.Kind.String())
	case <-time.After(50 * time.Millisecond):
	}

	close(resolveRelease)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handleESIMSwitchAfter did not finish after SIMAuth ready")
	}

	want := []string{"switch_end:dev-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("submitted commands = %v, want %v", got, want)
	}
	if len(be.setCalls) != 0 {
		t.Fatalf("setCalls=%v want none because switch_end owns VoWiFi restore", be.setCalls)
	}
	if len(be.resolveSIMAuthApps) != 1 || be.resolveSIMAuthApps[0] != "usim" {
		t.Fatalf("resolveSIMAuthApps=%v want [usim]", be.resolveSIMAuthApps)
	}
	if len(be.openChannelAIDs) != 0 {
		t.Fatalf("openChannelAIDs=%v want no open-channel validation", be.openChannelAIDs)
	}
}

func TestHandleESIMSwitchAfterBlocksVoWiFiWhenSIMAuthStillNotReady(t *testing.T) {
	withFastPostSwitchSIMAuthRecovery(t)

	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &esimSwitchRestoreBackendStub{
		mode:               backend.BackendQMI,
		getMode:            backend.ModeOnline,
		liveICCID:          "460001234567890",
		liveIMSI:           "460001234567890",
		resolvedSIMAuthErr: errors.New("sim_auth_aid_not_ready"),
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{FlightModeBefore: false})

	var got []string
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	p.handleESIMSwitchAfter("dev-1", 0)

	if len(got) != 0 {
		t.Fatalf("unexpected lifecycle commands when SIMAuth is not ready: %v", got)
	}
	if len(be.setCalls) != 1 || be.setCalls[0] != backend.ModeOnline {
		t.Fatalf("setCalls=%v want [%v]", be.setCalls, backend.ModeOnline)
	}
	if len(be.resolveSIMAuthApps) != 3 {
		t.Fatalf("resolveSIMAuthApps len=%d want 3", len(be.resolveSIMAuthApps))
	}
	if len(be.openChannelAIDs) != 0 {
		t.Fatalf("openChannelAIDs=%v want no open-channel validation", be.openChannelAIDs)
	}
}

func TestPostSwitchSIMAuthPrewarmFailureDoesNotRequireModemReboot(t *testing.T) {
	logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(t.TempDir(), "app.log")})
	withFastPostSwitchSIMAuthRecovery(t)

	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{
		mode:               backend.BackendQMI,
		getMode:            backend.ModeOnline,
		liveICCID:          "460001234567890",
		liveIMSI:           "460001234567890",
		resolvedSIMAuthErr: errors.New("sim_auth_aid_not_ready"),
	}
	p.workers["dev-1"] = &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{FlightModeBefore: false})

	ch := logger.GlobalBroadcaster.Subscribe()
	defer logger.GlobalBroadcaster.Unsubscribe(ch)

	p.prewarmPostSwitchSIMAuth("dev-1", p.workers["dev-1"])

	entry := waitLogEntry(t, ch, func(entry logger.LogEntry) bool {
		return strings.Contains(entry.Message, "切卡后 SIMAuth 预热未就绪")
	})
	fields := readLogFields(t, entry)
	if fields["need_modem_reboot"] == true {
		t.Fatal("SIMAuth prewarm failure should not mark need_modem_reboot=true")
	}
}

func TestHandleESIMSwitchBeforeClearsQMIAPDUSessionsForSwitch(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline}
	qmiMgr := &qmicore.Manager{}
	setPrivateMapFieldSwitchRestore(t, qmiMgr, "apduSessions", byte(1), func(elemType reflect.Type) reflect.Value {
		elem := reflect.New(elemType).Elem()
		elem.FieldByName("Channel").SetUint(1)
		elem.FieldByName("Owner").SetString("test")
		elem.FieldByName("OpenedAt").Set(reflect.ValueOf(time.Now()))
		return elem
	})
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
		QMICore: qmiMgr,
	}
	p.workers["dev-1"] = w
	p.voWiFiRuntimeStore().SetInstance("dev-1", &runtimehost.Instance{})

	p.handleESIMSwitchBefore("dev-1", "")

	if got := privateMapLenSwitchRestore(t, qmiMgr, "apduSessions"); got != 0 {
		t.Fatalf("apduSessions len=%d want 0", got)
	}
}

func TestHandleESIMSwitchBeforeClearsVisibleSIMIdentity(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	w := &Worker{
		ID:     "dev-1",
		Config: config.DeviceConfig{ID: "dev-1"},
	}
	w.cacheMu.Lock()
	w.state.Identity.ICCID = "old-iccid"
	w.state.Identity.IMSI = "old-imsi"
	w.state.Identity.NativeSPN = "old-spn"
	w.state.Identity.NativeMCC = "204"
	w.state.Identity.NativeMNC = "04"
	w.state.Identity.OPL = []backend.OPLRecord{{Record: 1, PLMN: "20404"}}
	w.state.Identity.Ready = true
	w.cacheMu.Unlock()
	p.workers["dev-1"] = w

	p.handleESIMSwitchBefore("dev-1", "target-iccid")

	status := w.ProjectDeviceStatus()
	if status.ICCID != "" || status.IMSI != "" || status.NativeSPN != "" || status.NativeMCC != "" || status.NativeMNC != "" || len(status.OPL) != 0 {
		t.Fatalf("visible SIM identity should be cleared during switch, got iccid=%q imsi=%q spn=%q native=%s/%s opl=%v",
			status.ICCID, status.IMSI, status.NativeSPN, status.NativeMCC, status.NativeMNC, status.OPL)
	}
	if w.SIMIdentityAllowsOverviewFallback() {
		t.Fatal("overview fallback should be disabled while switch identity is converging")
	}
}

func TestSIMIdentityGenerationSeparatesRepeatedSwitchesToSameTarget(t *testing.T) {
	w := &Worker{ID: "dev-1"}

	first := w.BeginSIMIdentityTransition("target-iccid", "switch_begin")
	ensured := w.EnsureSIMIdentityTransition("target-iccid", "post_switch_finalize")
	second := w.BeginSIMIdentityTransition("target-iccid", "switch_begin")

	if first == 0 {
		t.Fatal("first generation should be non-zero")
	}
	if ensured != first {
		t.Fatalf("EnsureSIMIdentityTransition generation=%d want first generation %d", ensured, first)
	}
	if second == first {
		t.Fatalf("new switch generation=%d should differ from previous generation", second)
	}
	if w.SIMIdentityConvergenceMatches("target-iccid", first) {
		t.Fatal("old generation should not match after a repeated switch to the same target")
	}
	if !w.SIMIdentityConvergenceMatches("target-iccid", second) {
		t.Fatal("current generation should match the active switch target")
	}
}

func TestHandleESIMSwitchAfterVoWiFiUsesCurrentSwitch(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	w.cacheMu.Lock()
	w.state.Identity.Ready = true
	w.state.Identity.ICCID = "460001234567890"
	w.state.Identity.IMSI = "460001234567890"
	w.cacheMu.Unlock()
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{
		VoWiFiActiveBefore: false,
		FlightModeBefore:   false,
	})

	p.handleESIMSwitchAfter("dev-1", 0)

	if len(be.setCalls) != 1 || be.setCalls[0] != backend.ModeOnline {
		t.Fatalf("expected data/radio fallback restore when VoWiFi identity gate blocks restore, got %v", be.setCalls)
	}
}

func TestHandleESIMSwitchAfterKeepsFlightWhenVoWiFiSwitchOff(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "460001234567890", liveIMSI: "460001234567890"}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: false},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{
		FlightModeBefore: true,
	})

	p.handleESIMSwitchAfter("dev-1", 0)

	if len(be.setCalls) != 1 || be.setCalls[0] != backend.ModeRFOff {
		t.Fatalf("setCalls=%v want [%v]", be.setCalls, backend.ModeRFOff)
	}
}

func TestHandleESIMSwitchAfterRestoresOnlineWhenVoWiFiSwitchOff(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "460001234567890", liveIMSI: "460001234567890"}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: false},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{
		FlightModeBefore: false,
	})

	p.handleESIMSwitchAfter("dev-1", 0)

	if len(be.setCalls) != 1 || be.setCalls[0] != backend.ModeOnline {
		t.Fatalf("setCalls=%v want [%v]", be.setCalls, backend.ModeOnline)
	}
}

func readLogFields(t *testing.T, entry logger.LogEntry) map[string]any {
	t.Helper()
	if strings.TrimSpace(entry.Fields) == "" {
		return map[string]any{}
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(entry.Fields), &fields); err != nil {
		t.Fatalf("failed to parse log fields: %v", err)
	}
	return fields
}

func waitLogEntry(t *testing.T, ch <-chan logger.LogEntry, match func(entry logger.LogEntry) bool) logger.LogEntry {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case entry := <-ch:
			if match(entry) {
				return entry
			}
		case <-deadline:
			t.Fatal("matched log entry not found")
		}
	}
}

func assertLogHasFieldNumber(t *testing.T, fields map[string]any, key string) {
	t.Helper()
	v, ok := fields[key]
	if !ok {
		t.Fatalf("missing log field %q", key)
	}
	if _, ok := v.(float64); !ok {
		t.Fatalf("log field %q should be number, got %T", key, v)
	}
}

func TestHandleESIMSwitchAfterLogsIdentitySemanticsAndStageDurations(t *testing.T) {
	logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(t.TempDir(), "app.log")})
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "new-iccid", liveIMSI: "new-imsi"}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: false},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{
		FlightModeBefore: false,
		ICCIDBefore:      "old-iccid",
		IMSIBefore:       "old-imsi",
	})

	ch := logger.GlobalBroadcaster.Subscribe()
	defer logger.GlobalBroadcaster.Unsubscribe(ch)

	p.handleESIMSwitchAfter("dev-1", 0)

	identityEntry := waitLogEntry(t, ch, func(entry logger.LogEntry) bool {
		return entry.Message == "[dev-1] 切卡后身份刷新完成，VoWiFi 恢复前已同步当前设备身份"
	})
	identityFields := readLogFields(t, identityEntry)
	if identityFields["identity_ready"] != true {
		t.Fatalf("identity_ready=%v want true", identityFields["identity_ready"])
	}
	if identityFields["iccid_changed"] != true {
		t.Fatalf("iccid_changed=%v want true", identityFields["iccid_changed"])
	}
	if identityFields["imsi_changed"] != true {
		t.Fatalf("imsi_changed=%v want true", identityFields["imsi_changed"])
	}

	timingEntry := waitLogEntry(t, ch, func(entry logger.LogEntry) bool {
		return entry.Message == "[dev-1] 切卡后 finalize 阶段耗时"
	})
	timingFields := readLogFields(t, timingEntry)
	assertLogHasFieldNumber(t, timingFields, "core_ready_wait_ms")
	assertLogHasFieldNumber(t, timingFields, "identity_refresh_ms")
	assertLogHasFieldNumber(t, timingFields, "runtime_refresh_ms")
	assertLogHasFieldNumber(t, timingFields, "finalize_total_ms")
}

func TestHandleESIMSwitchAfterStrictICCIDGateBlocksVoWiFiRestore(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveIMSI: "460001234567890"}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{VoWiFiActiveBefore: true})

	p.handleESIMSwitchAfter("dev-1", 0)

	if len(be.setCalls) != 1 || be.setCalls[0] != backend.ModeOnline {
		t.Fatalf("expected data/radio fallback restore when identity gate blocks VoWiFi, got %v", be.setCalls)
	}
}

func TestHandleESIMSwitchAfterStrictICCIDGateDoesNotScheduleDeferredRecover(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveIMSI: "460001234567890"}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{VoWiFiActiveBefore: true})

	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.handleESIMSwitchAfter("dev-1", 0)

	select {
	case cmd := <-commands:
		t.Fatalf("unexpected deferred recover command after identity gate failure: %s", cmd.Kind.String())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHandleESIMSwitchAfterStrictICCIDGateIgnoresStaleCachedICCID(t *testing.T) {
	logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(t.TempDir(), "app.log")})
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveIMSI: "460001234567890"}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	w.cacheMu.Lock()
	w.state.Identity.Ready = true
	w.state.Identity.ICCID = "stale-old-iccid"
	w.state.Identity.IMSI = "stale-old-imsi"
	w.cacheMu.Unlock()
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{VoWiFiActiveBefore: true})

	p.handleESIMSwitchAfter("dev-1", 0)

	if len(be.setCalls) != 1 || be.setCalls[0] != backend.ModeOnline {
		t.Fatalf("expected data/radio fallback restore when identity gate blocks VoWiFi, got %v", be.setCalls)
	}
}

func TestWakeDesiredVoWiFiRecoverFromDeviceEventSubmitsRecoverWhenIdle(t *testing.T) {
	withDeviceEventRecoverWakeDelay(t, 10*time.Millisecond)
	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "460001234567890", liveIMSI: "460001234567890"}
	p.SetPolicyResolver(&stubPolicyResolver{
		pol: cardpolicy.Policy{ICCID: "460001234567890", VoWiFiEnabled: true},
	})
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	w.state.Identity.ICCID = "460001234567890"
	w.state.Identity.IMSI = "460001234567890"
	p.workers["dev-1"] = w

	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.wakeDesiredVoWiFiRecoverFromDeviceEvent("dev-1", "post_switch_uim_slot_status")

	select {
	case cmd := <-commands:
		if cmd.Kind != vowifihost.LifecycleCommandRecover {
			t.Fatalf("kind=%s want recover", cmd.Kind.String())
		}
		if cmd.Reason != "post_switch_uim_slot_status" {
			t.Fatalf("reason=%q want post_switch_uim_slot_status", cmd.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("recover was not submitted")
	}
}

func TestWakeDesiredVoWiFiRecoverFromDeviceEventSkipsWhenRuntimeHostInstanceActive(t *testing.T) {
	withDeviceEventRecoverWakeDelay(t, 10*time.Millisecond)
	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "460001234567890", liveIMSI: "460001234567890"}
	p.workers["dev-1"] = &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.voWiFiRuntimeStore().SetInstance("dev-1", &runtimehost.Instance{})
	t.Cleanup(func() { p.voWiFiRuntimeStore().DeleteInstance("dev-1", nil) })

	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.wakeDesiredVoWiFiRecoverFromDeviceEvent("dev-1", "post_switch_uim_slot_status")

	select {
	case cmd := <-commands:
		t.Fatalf("unexpected recover with active runtimehost instance: %s", cmd.Kind.String())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWakeDesiredVoWiFiRecoverFromDeviceEventSkipsDuringSwitch(t *testing.T) {
	withDeviceEventRecoverWakeDelay(t, 10*time.Millisecond)
	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "460001234567890", liveIMSI: "460001234567890"}
	p.workers["dev-1"] = &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{VoWiFiActiveBefore: true})

	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.wakeDesiredVoWiFiRecoverFromDeviceEvent("dev-1", "post_switch_uim_slot_status")

	select {
	case cmd := <-commands:
		t.Fatalf("unexpected recover while switching: %s", cmd.Kind.String())
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRefreshPostSwitchIdentityClearsOldSIMMetadataWithoutReadingNewMetadata(t *testing.T) {
	p := NewPool(&config.Config{})
	metadataStarted := make(chan struct{})
	be := &esimSwitchRestoreBackendStub{
		mode:            backend.BackendQMI,
		liveICCID:       "new-iccid",
		liveIMSI:        "new-imsi",
		metadataStarted: metadataStarted,
		simMetadata: &backend.SIMMetadata{
			NativeMCC: "515",
			NativeMNC: "66",
			OPL:       []backend.OPLRecord{{Record: 1, PLMN: "51566"}},
		},
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1"},
		Backend: be,
	}
	w.cacheMu.Lock()
	w.state.Identity.ICCID = "old-iccid"
	w.state.Identity.IMSI = "old-imsi"
	w.state.Identity.NativeMCC = "204"
	w.state.Identity.NativeMNC = "04"
	w.state.Identity.OPL = []backend.OPLRecord{{Record: 1, PLMN: "20404"}}
	w.cacheMu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, err := p.refreshPostSwitchIdentity("dev-1", w, esimSwitchContext{
			ICCIDBefore: "old-iccid",
			IMSIBefore:  "old-imsi",
		})
		if err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("refreshPostSwitchIdentity() err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for identity refresh")
	}

	select {
	case <-metadataStarted:
		t.Fatal("post-switch critical identity refresh should not read optional metadata synchronously")
	default:
	}

	status := w.ProjectDeviceStatus()
	if status.ICCID != "new-iccid" || status.IMSI != "new-imsi" {
		t.Fatalf("identity=%q/%q want new-iccid/new-imsi", status.ICCID, status.IMSI)
	}
	if status.NativeMCC != "" || status.NativeMNC != "" || len(status.OPL) != 0 {
		t.Fatalf("old SIM metadata should be cleared on identity change, got mcc/mnc=%s/%s opl=%v", status.NativeMCC, status.NativeMNC, status.OPL)
	}
}

func TestRefreshPostSwitchIdentityDoesNotRequireSPNForVoWiFiRestore(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{
		mode:               backend.BackendQMI,
		getMode:            backend.ModeOnline,
		liveICCID:          "460001234567890",
		liveIMSI:           "460001234567890",
		liveSPNErr:         fmt.Errorf("QMI error: service=0x0b msg=0x0020 result=0x0001 error=0x0010"),
		resolvedSIMAuthAID: "A0000000871002FFFFFFFF8903020000",
		resolvedSIMAuthSrc: "qmi_card_status",
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{ICCIDBefore: "old", IMSIBefore: "old"})

	var got []string
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		got = append(got, cmd.Kind.String()+":"+cmd.DeviceID)
		return nil
	}

	p.handleESIMSwitchAfter("dev-1", 0)

	want := []string{"switch_end:dev-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("submitted commands = %v, want %v", got, want)
	}
}

func TestRefreshPostSwitchIdentityRejectsStaleLiveIdentityWhenTargetKnown(t *testing.T) {
	withFastPostSwitchIdentityPolling(t)
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{
		mode:      backend.BackendQMI,
		liveICCID: "old-iccid",
		liveIMSI:  "old-imsi",
		simMetadata: &backend.SIMMetadata{
			NativeMCC: "515",
			NativeMNC: "66",
			OPL:       []backend.OPLRecord{{Record: 1, PLMN: "51566"}},
		},
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1"},
		Backend: be,
	}
	w.cacheMu.Lock()
	w.state.Identity.ICCID = "old-iccid"
	w.state.Identity.IMSI = "old-imsi"
	w.state.Identity.NativeMCC = "204"
	w.state.Identity.NativeMNC = "04"
	w.state.Identity.OPL = []backend.OPLRecord{{Record: 1, PLMN: "20404"}}
	w.cacheMu.Unlock()

	ready, err := p.refreshPostSwitchIdentity("dev-1", w, esimSwitchContext{
		ICCIDBefore: "old-iccid",
		IMSIBefore:  "old-imsi",
		TargetICCID: "target-iccid",
	})
	if err == nil {
		t.Fatal("refreshPostSwitchIdentity() err=nil, want stale identity rejection")
	}
	if ready {
		t.Fatal("ready=true, want false for stale live identity")
	}
	status := w.ProjectDeviceStatus()
	if status.ICCID == "old-iccid" || status.IMSI == "old-imsi" || status.NativeMCC != "" || status.NativeMNC != "" || len(status.OPL) != 0 {
		t.Fatalf("stale identity should stay cleared after rejected refresh, got iccid=%q imsi=%q native=%s/%s opl=%v",
			status.ICCID, status.IMSI, status.NativeMCC, status.NativeMNC, status.OPL)
	}
}

func TestHandleESIMSwitchAfterMarksDegradedWhenTargetICCIDStaysOld(t *testing.T) {
	withFastPostSwitchIdentityPolling(t)
	logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(t.TempDir(), "app.log")})
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{
		mode:      backend.BackendQMI,
		getMode:   backend.ModeOnline,
		liveICCID: "old-iccid",
		liveIMSI:  "old-imsi",
	}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: false},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{
		FlightModeBefore: false,
		ICCIDBefore:      "old-iccid",
		IMSIBefore:       "old-imsi",
		TargetICCID:      "target-iccid",
	})

	ch := logger.GlobalBroadcaster.Subscribe()
	defer logger.GlobalBroadcaster.Unsubscribe(ch)

	p.handleESIMSwitchAfter("dev-1", 0)

	degradedEntry := waitLogEntry(t, ch, func(entry logger.LogEntry) bool {
		if entry.Message != "[dev-1] eSIM 切卡阶段更新" {
			return false
		}
		fields := readLogFields(t, entry)
		return fields["switch_phase"] == string(esim.SwitchPhaseDegraded)
	})
	fields := readLogFields(t, degradedEntry)
	if fields["switch_phase"] != string(esim.SwitchPhaseDegraded) {
		t.Fatalf("switch_phase=%v want %s", fields["switch_phase"], esim.SwitchPhaseDegraded)
	}
}

func TestHandleESIMSwitchAfterStillConvergesIdentityWhenControlUnavailable(t *testing.T) {
	withImmediatePostSwitchIdentityRetries(t)
	withFastPostSwitchIdentityPolling(t)
	p := NewPool(&config.Config{})
	defer p.cancel()
	deviceID := "dev-1"
	be := &esimSwitchRestoreBackendStub{
		mode:      backend.BackendQMI,
		getMode:   backend.ModeOnline,
		liveICCID: "target-iccid",
		liveIMSI:  "target-imsi",
		uimReadiness: []qmimanager.UIMReadiness{{
			TransportReady: false,
			ControlReady:   false,
			Reason:         qmimanager.UIMReadinessControlUnavailable,
		}},
	}
	w := &Worker{
		ID:      deviceID,
		Config:  config.DeviceConfig{ID: deviceID, VoWiFiEnabled: false},
		Backend: be,
	}
	w.cacheMu.Lock()
	w.state.Identity.ICCID = "old-iccid"
	w.state.Identity.IMSI = "old-imsi"
	w.state.Identity.Ready = true
	w.cacheMu.Unlock()
	p.workers[deviceID] = w
	withSwitchSnapshot(p, deviceID, esimSwitchContext{
		ICCIDBefore: "old-iccid",
		IMSIBefore:  "old-imsi",
		TargetICCID: "target-iccid",
	})

	p.handleESIMSwitchAfter(deviceID, 0)

	deadline := time.After(500 * time.Millisecond)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		status := w.ProjectDeviceStatus()
		if status.ICCID == "target-iccid" && status.IMSI == "target-imsi" {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("identity did not converge after degraded control readiness, got %+v", status)
		case <-ticker.C:
		}
	}
}

func TestHandleESIMSwitchAfterRestoreDoesNotSelfBlockOnSwitchGate(t *testing.T) {
	logger.Setup(logger.LogConfig{Debug: true, Filename: filepath.Join(t.TempDir(), "app.log")})
	p := NewPool(&config.Config{})
	deviceID := "dev-1"
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "460001234567890", liveIMSI: "460001234567890"}
	w := &Worker{
		ID:      deviceID,
		Config:  config.DeviceConfig{ID: deviceID, VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers[deviceID] = w
	withSwitchSnapshot(p, deviceID, esimSwitchContext{
		VoWiFiActiveBefore: true,
		SwitchToken:        1,
		ICCIDBefore:        "old-iccid",
		IMSIBefore:         "old-imsi",
	})
	p.switchMu.Lock()
	p.switchingDevices[deviceID] = true
	p.switchTokens[deviceID] = 1
	p.switchMu.Unlock()

	ch := logger.GlobalBroadcaster.Subscribe()
	defer logger.GlobalBroadcaster.Unsubscribe(ch)

	p.handleESIMSwitchAfter(deviceID, 1)

	timeout := time.After(300 * time.Millisecond)
	for {
		select {
		case entry := <-ch:
			if entry.Message != "切卡后恢复 VoWiFi 失败" {
				continue
			}
			fields := readLogFields(t, entry)
			errMsg, _ := fields["err"].(string)
			if strings.Contains(errMsg, "正在切卡") {
				t.Fatalf("unexpected restore failure blocked by switch gate: %v", fields["err"])
			}
		case <-timeout:
			return
		}
	}
}

func TestHandleESIMSwitchAfterBroadcastsRuntimeStateAfterFinalize(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "460001234567890", liveIMSI: "460001234567890"}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: false},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{FlightModeBefore: false})

	ch, unsub := p.SubscribeVoWiFiState("dev-1")
	defer unsub()

	p.handleESIMSwitchAfter("dev-1", 0)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("expected switch-finalize state broadcast")
	}
}

func TestHandleESIMSwitchAfterKeepsSwitchingFlagUntilFinalizeEnd(t *testing.T) {
	p := NewPool(&config.Config{})
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeOnline, liveICCID: "460001234567890", liveIMSI: "460001234567890"}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: false},
		Backend: be,
	}
	p.workers["dev-1"] = w
	withSwitchSnapshot(p, "dev-1", esimSwitchContext{FlightModeBefore: false, SwitchToken: 1})
	p.switchMu.Lock()
	p.switchingDevices["dev-1"] = true
	p.switchTokens["dev-1"] = 1
	p.switchMu.Unlock()

	done := make(chan struct{}, 1)
	go func() {
		p.handleESIMSwitchAfter("dev-1", 1)
		done <- struct{}{}
	}()

	select {
	case <-done:
		// finalized
	case <-time.After(2 * time.Second):
		t.Fatal("handleESIMSwitchAfter did not finish")
	}

	if p.IsESIMSwitching("dev-1") {
		t.Fatal("expected switching flag cleared after finalize")
	}
}

func TestHandleESIMSwitchFailedClearsSwitchingAndRestoresRadioData(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &esimSwitchRestoreBackendStub{mode: backend.BackendQMI, getMode: backend.ModeRFOff}
	w := &Worker{
		ID:      "dev-1",
		Config:  config.DeviceConfig{ID: "dev-1", VoWiFiEnabled: true},
		Backend: be,
	}
	p.workers["dev-1"] = w
	snapshot := esimSwitchContext{
		SwitchToken:          7,
		FlightModeBefore:     false,
		NetworkEnabledBefore: true,
	}
	withSwitchSnapshot(p, "dev-1", snapshot)
	p.switchMu.Lock()
	p.switchTokens["dev-1"] = snapshot.SwitchToken
	p.switchMu.Unlock()

	p.handleESIMSwitchFailed("dev-1", 0)

	if p.IsESIMSwitching("dev-1") {
		t.Fatal("expected switching flag cleared after switch failure")
	}
	if len(be.setCalls) != 1 || be.setCalls[0] != backend.ModeOnline {
		t.Fatalf("setCalls=%v want [%v]", be.setCalls, backend.ModeOnline)
	}
}

func TestHandleESIMSwitchFailedWithErrorClearsSwitchingWithoutQMIRecovery(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	deviceID := "dev-1"
	snapshot := p.beginESIMSwitch(deviceID, "")

	p.handleESIMSwitchFailedWithError(deviceID, snapshot.SwitchToken, errors.New("write failed: write unix @->@qmi-proxy: write: broken pipe"))

	if p.IsESIMSwitching(deviceID) {
		t.Fatal("expected switching flag cleared after switch failure")
	}
	waitForModemRebootRecoverySwitchRestore(t, p, deviceID, false)
}

func TestRefreshPostSwitchIdentityWaitsForIMSIAfterICCIDMatch(t *testing.T) {
	// 验证修复：切卡后 ICCID 快速生效，但 IMSI 仍在 USIM 重初始化窗口（0x0030/0x0025）。
	// 轮询不应该在 ICCID 命中后立即退出，应该等 IMSI 也成功读出。
	p := NewPool(&config.Config{})
	defer p.cancel()

	// 模拟 ICCID 快速生效但 IMSI 延迟的情况
	imsiCallsMu := &sync.Mutex{}
	var imsiCalls int

	be := &imsiDelayedBackendStub{
		esimSwitchRestoreBackendStub: esimSwitchRestoreBackendStub{
			mode:      backend.BackendQMI,
			liveICCID: "target-iccid-123",
			liveIMSI:  "",
		},
		imsiCallsMu: imsiCallsMu,
		imsiCalls:   &imsiCalls,
	}

	worker := &Worker{ID: "dev-1", Config: config.DeviceConfig{ID: "dev-1"}}
	worker.Backend = be

	p.workers["dev-1"] = worker
	worker.cacheMu.Lock()
	worker.state.Identity.ICCID = "old-iccid"
	worker.state.Identity.IMSI = "old-imsi"
	worker.cacheMu.Unlock()

	// 调用身份刷新
	ok, err := p.refreshPostSwitchIdentity("dev-1", worker, esimSwitchContext{TargetICCID: "target-iccid-123"})

	if !ok {
		t.Fatalf("refreshPostSwitchIdentity failed: %v", err)
	}

	// 验证最终缓存中 IMSI 不是空
	worker.cacheMu.Lock()
	finalIMSI := worker.state.Identity.IMSI
	finalICCID := worker.state.Identity.ICCID
	worker.cacheMu.Unlock()

	if finalICCID != "target-iccid-123" {
		t.Fatalf("final ICCID=%q want target-iccid-123", finalICCID)
	}
	if finalIMSI != "234159608701764" {
		t.Fatalf("final IMSI=%q want 234159608701764 (not empty)", finalIMSI)
	}
	// 验证轮询至少调用了 3 次（不是 1 次，这证明了等待 IMSI 就绪的修复生效）
	imsiCallsMu.Lock()
	calls := imsiCalls
	imsiCallsMu.Unlock()
	if calls < 3 {
		t.Fatalf("IMSI read calls=%d want >= 3 (should wait for IMSI to be ready)", calls)
	}
}

// imsiDelayedBackendStub 模拟 IMSI 延迟就绪的情况
type imsiDelayedBackendStub struct {
	esimSwitchRestoreBackendStub
	imsiCallsMu *sync.Mutex
	imsiCalls   *int
}

func (s *imsiDelayedBackendStub) GetIMSILive(ctx context.Context) (string, error) {
	s.imsiCallsMu.Lock()
	call := *s.imsiCalls
	*s.imsiCalls++
	s.imsiCallsMu.Unlock()
	// 前 2 次返回错误（USIM 重初始化窗口），第 3 次成功
	if call < 2 {
		return "", fmt.Errorf("QMI error: service=0x0b error=0x0030")
	}
	return "234159608701764", nil
}
