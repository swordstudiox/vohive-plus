package device

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestPostSwitchDecisionTransportFatalWaitsForControl(t *testing.T) {
	r := qmimanager.UIMReadiness{
		TransportReady: false,
		ControlReady:   false,
		Reason:         qmimanager.UIMReadinessTransportFatal,
		Err:            errors.New("QMI: read failed: EOF"),
	}

	got := classifyPostSwitchReadiness(r, "8985203103011907194")

	if got.Action != postSwitchActionWaitControl {
		t.Fatalf("action=%q want %q", got.Action, postSwitchActionWaitControl)
	}
}

func TestPostSwitchDecisionSIMBlockedKeepsConverging(t *testing.T) {
	r := qmimanager.UIMReadiness{
		TransportReady: true,
		ControlReady:   true,
		SIMStatus:      qmi.SIMBlocked,
		Reason:         qmimanager.UIMReadinessSIMBlocked,
	}

	got := classifyPostSwitchReadiness(r, "8985203103011907194")

	if got.Action != postSwitchActionWaitSIM {
		t.Fatalf("action=%q want %q", got.Action, postSwitchActionWaitSIM)
	}
}

func TestPostSwitchDecisionTargetICCIDReadyRestoresRuntime(t *testing.T) {
	r := qmimanager.UIMReadiness{
		TransportReady: true,
		ControlReady:   true,
		UIMReady:       true,
		SIMStatus:      qmi.SIMReady,
		Reason:         qmimanager.UIMReadinessReady,
		ICCID:          "8985203103011907194",
		IMSI:           "460011234567890",
	}

	got := classifyPostSwitchReadiness(r, "8985203103011907194")

	if got.Action != postSwitchActionRestoreRuntime {
		t.Fatalf("action=%q want %q", got.Action, postSwitchActionRestoreRuntime)
	}
}

func TestPostSwitchDecisionOldICCIDWaitsIdentity(t *testing.T) {
	r := qmimanager.UIMReadiness{
		TransportReady: true,
		ControlReady:   true,
		UIMReady:       true,
		SIMStatus:      qmi.SIMReady,
		Reason:         qmimanager.UIMReadinessReady,
		ICCID:          "8964240002094346553",
		IMSI:           "530240209434655",
	}

	got := classifyPostSwitchReadiness(r, "8985203103011907194")

	if got.Action != postSwitchActionWaitIdentity {
		t.Fatalf("action=%q want %q", got.Action, postSwitchActionWaitIdentity)
	}
}

type postSwitchReadinessStub struct {
	readiness []qmimanager.UIMReadiness
	calls     int
}

func (s *postSwitchReadinessStub) GetUIMReadiness(ctx context.Context) (qmimanager.UIMReadiness, error) {
	if s.calls >= len(s.readiness) {
		return s.readiness[len(s.readiness)-1], nil
	}
	out := s.readiness[s.calls]
	s.calls++
	return out, out.Err
}

type postSwitchPowerStub struct {
	offCalls int
	onCalls  int
	offErr   error
	onErr    error
}

type postSwitchCompositeReloadStub struct {
	slot uint8
	err  error
}

type postSwitchCoreRecoveryStub struct {
	postSwitchPowerStub
	requests []string
}

type postSwitchServiceProbeStub struct {
	postSwitchCoreRecoveryStub
	modeErrs  []error
	modeCalls int
}

type postSwitchRunBackendStub struct {
	backend.DeviceBackend
	readiness      []qmimanager.UIMReadiness
	calls          int
	offCalls       int
	onCalls        int
	worker         *Worker
	sawEventSource bool
}

func (s *postSwitchCoreRecoveryStub) RequestCoreRecovery(reason string) bool {
	s.requests = append(s.requests, reason)
	return true
}

func (s *postSwitchCoreRecoveryStub) WaitCoreReady(ctx context.Context) error {
	return nil
}

func (s *postSwitchServiceProbeStub) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	idx := s.modeCalls
	s.modeCalls++
	if idx >= len(s.modeErrs) {
		return backend.ModeOnline, nil
	}
	return backend.ModeOnline, s.modeErrs[idx]
}

func (s *postSwitchRunBackendStub) GetUIMReadiness(ctx context.Context) (qmimanager.UIMReadiness, error) {
	if s.worker != nil && s.worker.currentSwitchEventSource() != nil {
		s.sawEventSource = true
	}
	if s.calls >= len(s.readiness) {
		out := s.readiness[len(s.readiness)-1]
		return out, out.Err
	}
	out := s.readiness[s.calls]
	s.calls++
	return out, out.Err
}

func (s *postSwitchRunBackendStub) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	return backend.ModeOnline, nil
}

func (s *postSwitchRunBackendStub) GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error) {
	return &backend.SignalInfo{}, nil
}

func (s *postSwitchCompositeReloadStub) UIMPostSwitchReload(ctx context.Context, readiness qmimanager.UIMReadiness, opts qmimanager.UIMPostSwitchReloadOptions) (uint8, error) {
	if s.err != nil {
		return 0, s.err
	}
	if opts.DefaultSlot != 1 {
		return 0, fmt.Errorf("default slot=%d want 1", opts.DefaultSlot)
	}
	if readiness.SlotKnown {
		s.slot = readiness.ActiveSlot
	} else {
		s.slot = opts.DefaultSlot
	}
	return s.slot, nil
}

func (s *postSwitchPowerStub) UIMPowerOffSIM(ctx context.Context, slot uint8) error {
	s.offCalls++
	return s.offErr
}

func (s *postSwitchPowerStub) UIMPowerOnSIM(ctx context.Context, slot uint8) error {
	s.onCalls++
	return s.onErr
}

func (s *postSwitchRunBackendStub) UIMPowerOffSIM(ctx context.Context, slot uint8) error {
	s.offCalls++
	if slot != 1 {
		return fmt.Errorf("power off slot=%d want 1", slot)
	}
	return nil
}

func (s *postSwitchRunBackendStub) UIMPowerOnSIM(ctx context.Context, slot uint8) error {
	s.onCalls++
	if slot != 1 {
		return fmt.Errorf("power on slot=%d want 1", slot)
	}
	return nil
}

func TestRunPostSwitchConvergenceFlagOffUsesLegacyPollingOnly(t *testing.T) {
	worker := &Worker{Config: config.DeviceConfig{}}
	backend := &postSwitchRunBackendStub{
		readiness: []qmimanager.UIMReadiness{{
			TransportReady: true,
			ControlReady:   true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			Reason:         qmimanager.UIMReadinessReady,
			ICCID:          "target",
			IMSI:           "imsi-target",
		}},
		worker: worker,
	}
	worker.Backend = backend

	result := (&Pool{}).runPostSwitchConvergence("dev-1", 7, worker, esimSwitchContext{TargetICCID: "target"})

	if !result.Ready {
		t.Fatalf("result=%+v want ready", result)
	}
	if backend.sawEventSource {
		t.Fatal("legacy path unexpectedly attached a switch event source")
	}
	if backend.offCalls != 0 || backend.onCalls != 0 {
		t.Fatalf("power calls off=%d on=%d want 0/0", backend.offCalls, backend.onCalls)
	}
	if src := worker.currentSwitchEventSource(); src != nil {
		t.Fatalf("switch event source still attached: %v", src)
	}
}

func TestRunPostSwitchConvergenceEventGatedSuccessReturnsBeforePollingFallback(t *testing.T) {
	worker := &Worker{Config: config.DeviceConfig{ESIMSwitch: config.ESIMSwitchConfig{
		EventGatedConverge: true,
		ReinitWindowMS:     500,
	}}}
	backend := &postSwitchRunBackendStub{
		readiness: []qmimanager.UIMReadiness{{
			TransportReady: true,
			ControlReady:   true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			Reason:         qmimanager.UIMReadinessReady,
			ICCID:          "target",
			IMSI:           "imsi-target",
		}},
		worker: worker,
	}
	worker.Backend = backend

	go func() {
		deadline := time.After(time.Second)
		for {
			if src := worker.currentSwitchEventSource(); src != nil {
				src.PublishRefresh(uimRefreshStageEndWithSuccess)
				return
			}
			select {
			case <-deadline:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()

	result := (&Pool{}).runPostSwitchConvergence("dev-1", 7, worker, esimSwitchContext{TargetICCID: "target"})

	if !result.Ready || result.Reason != "ready" {
		t.Fatalf("result=%+v want ready reason=ready from event-gated convergence", result)
	}
	if !backend.sawEventSource {
		t.Fatal("event-gated path did not expose switch event source during readiness confirmation")
	}
	if backend.offCalls != 0 || backend.onCalls != 0 {
		t.Fatalf("power calls off=%d on=%d want 0/0", backend.offCalls, backend.onCalls)
	}
	if src := worker.currentSwitchEventSource(); src != nil {
		t.Fatalf("switch event source still attached: %v", src)
	}
}

func TestRunPostSwitchConvergenceEventGatedTimeoutPowerCyclesThenPolls(t *testing.T) {
	worker := &Worker{Config: config.DeviceConfig{ESIMSwitch: config.ESIMSwitchConfig{
		EventGatedConverge: true,
		ReinitWindowMS:     1,
	}}}
	backend := &postSwitchRunBackendStub{
		readiness: []qmimanager.UIMReadiness{{
			TransportReady: true,
			ControlReady:   true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			Reason:         qmimanager.UIMReadinessReady,
			ICCID:          "target",
			IMSI:           "imsi-target",
		}},
		worker: worker,
	}
	worker.Backend = backend

	result := (&Pool{}).runPostSwitchConvergence("dev-1", 7, worker, esimSwitchContext{TargetICCID: "target"})

	if !result.Ready {
		t.Fatalf("result=%+v want ready after timeout fallback and polling", result)
	}
	if backend.offCalls != 1 || backend.onCalls != 1 {
		t.Fatalf("power calls off=%d on=%d want 1/1", backend.offCalls, backend.onCalls)
	}
	if src := worker.currentSwitchEventSource(); src != nil {
		t.Fatalf("switch event source still attached: %v", src)
	}
}

func TestConvergePostSwitchRequestsCoreRecoveryOnConsecutiveQMIStalls(t *testing.T) {
	readiness := &postSwitchReadinessStub{readiness: []qmimanager.UIMReadiness{
		{
			TransportReady: true,
			ControlReady:   false,
			Reason:         qmimanager.UIMReadinessControlUnavailable,
			Err:            context.DeadlineExceeded,
		},
		{
			TransportReady: true,
			ControlReady:   false,
			Reason:         qmimanager.UIMReadinessControlUnavailable,
			Err:            context.DeadlineExceeded,
		},
		{
			TransportReady: true,
			ControlReady:   true,
			CardPresent:    true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			ActiveSlot:     1,
			SlotKnown:      true,
			ICCID:          "target",
			IMSI:           "imsi-target",
			Reason:         qmimanager.UIMReadinessReady,
		},
	}}
	recoverer := &postSwitchCoreRecoveryStub{}

	result := convergePostSwitch(context.Background(), readiness, recoverer, postSwitchConvergenceOptions{
		TargetICCID:         "target",
		IdentityAttempts:    3,
		ReloadAfterAttempts: 1,
		ProbeTimeout:        time.Second,
	})

	if !result.Ready {
		t.Fatalf("result=%+v want ready after core recovery request and target identity convergence", result)
	}
	if len(recoverer.requests) != 1 || recoverer.requests[0] != "post_switch_qmi_service_stalled" {
		t.Fatalf("core recovery requests=%v want one post_switch_qmi_service_stalled", recoverer.requests)
	}
	if recoverer.offCalls != 0 || recoverer.onCalls != 0 {
		t.Fatalf("SIM power cycle calls off=%d on=%d want none for QMI service stall", recoverer.offCalls, recoverer.onCalls)
	}
}

func TestConvergePostSwitchRequestsCoreRecoveryOnServiceProbeTimeout(t *testing.T) {
	readiness := &postSwitchReadinessStub{readiness: []qmimanager.UIMReadiness{
		{
			TransportReady: true,
			ControlReady:   true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			ActiveSlot:     1,
			SlotKnown:      true,
			Reason:         qmimanager.UIMReadinessIdentityEmpty,
		},
		{
			TransportReady: true,
			ControlReady:   true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			ActiveSlot:     1,
			SlotKnown:      true,
			Reason:         qmimanager.UIMReadinessIdentityEmpty,
		},
		{
			TransportReady: true,
			ControlReady:   true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			ActiveSlot:     1,
			SlotKnown:      true,
			ICCID:          "target",
			IMSI:           "imsi-target",
			Reason:         qmimanager.UIMReadinessReady,
		},
	}}
	recoverer := &postSwitchServiceProbeStub{
		modeErrs: []error{context.DeadlineExceeded, context.DeadlineExceeded, nil},
	}

	result := convergePostSwitch(context.Background(), readiness, recoverer, postSwitchConvergenceOptions{
		TargetICCID:         "target",
		IdentityAttempts:    3,
		ReloadAfterAttempts: 3,
		ProbeTimeout:        time.Second,
	})

	if !result.Ready {
		t.Fatalf("result=%+v want ready after service probe core recovery request", result)
	}
	if len(recoverer.requests) != 1 || recoverer.requests[0] != "post_switch_qmi_service_stalled" {
		t.Fatalf("core recovery requests=%v want one post_switch_qmi_service_stalled", recoverer.requests)
	}
	if recoverer.offCalls != 0 || recoverer.onCalls != 0 {
		t.Fatalf("SIM power cycle calls off=%d on=%d want none for QMI service stall", recoverer.offCalls, recoverer.onCalls)
	}
}

func TestConvergePostSwitchSuppressesRecoveryInsideReinitWindow(t *testing.T) {
	rdy := &postSwitchReadinessStub{readiness: []qmimanager.UIMReadiness{{
		TransportReady: false,
		ControlReady:   false,
		Reason:         qmimanager.UIMReadinessControlUnavailable,
		Err:            context.DeadlineExceeded,
	}}}
	rec := &postSwitchCoreRecoveryStub{}

	convergePostSwitch(context.Background(), rdy, rec, postSwitchConvergenceOptions{
		TargetICCID:        "89441000400308626482",
		IdentityAttempts:   3,
		ProbeTimeout:       10 * time.Millisecond,
		CoreStallThreshold: 2,
		ReinitWindow:       5 * time.Second,
	})

	if len(rec.requests) != 0 {
		t.Fatalf("expected NO core recovery inside reinit window, got %v", rec.requests)
	}
}

func TestConvergePostSwitchAllowsRecoveryAfterReinitWindow(t *testing.T) {
	rdy := &postSwitchReadinessStub{readiness: []qmimanager.UIMReadiness{{
		TransportReady: false,
		ControlReady:   false,
		Reason:         qmimanager.UIMReadinessControlUnavailable,
		Err:            context.DeadlineExceeded,
	}}}
	rec := &postSwitchCoreRecoveryStub{}

	convergePostSwitch(context.Background(), rdy, rec, postSwitchConvergenceOptions{
		TargetICCID:        "89441000400308626482",
		IdentityAttempts:   5,
		ProbeTimeout:       10 * time.Millisecond,
		CoreStallThreshold: 2,
		ReinitWindow:       0,
	})

	if len(rec.requests) == 0 {
		t.Fatal("expected core recovery to fire when reinit window is closed")
	}
}

func TestPostSwitchConvergenceReloadTimeoutIsDegradedNotRecovery(t *testing.T) {
	readiness := &postSwitchReadinessStub{readiness: []qmimanager.UIMReadiness{{
		TransportReady: true,
		ControlReady:   true,
		UIMReady:       true,
		SIMStatus:      qmi.SIMReady,
		SlotKnown:      true,
		ActiveSlot:     1,
		Reason:         qmimanager.UIMReadinessIdentityEmpty,
	}}}
	power := &postSwitchPowerStub{offErr: context.DeadlineExceeded}

	result := convergePostSwitch(context.Background(), readiness, power, postSwitchConvergenceOptions{
		TargetICCID:         "8985203103011907194",
		IdentityAttempts:    1,
		ReloadAfterAttempts: 1,
	})

	if !result.Degraded || result.Reason != "reload_degraded" {
		t.Fatalf("result=%+v, want reload_degraded", result)
	}
	if power.offCalls != 1 {
		t.Fatalf("power off calls=%d want 1", power.offCalls)
	}
}

func TestConvergePostSwitchNonEventGatedUsesSharedPowerCycleHelper(t *testing.T) {
	readiness := &postSwitchReadinessStub{readiness: []qmimanager.UIMReadiness{{
		TransportReady: true,
		ControlReady:   true,
		UIMReady:       true,
		SIMStatus:      qmi.SIMReady,
		SlotKnown:      true,
		ActiveSlot:     1,
		Reason:         qmimanager.UIMReadinessIdentityEmpty,
	}}}
	power := &postSwitchPowerStub{offErr: context.DeadlineExceeded}

	result := convergePostSwitch(context.Background(), readiness, power, postSwitchConvergenceOptions{
		TargetICCID:         "target",
		IdentityAttempts:    1,
		ReloadAfterAttempts: 1,
	})
	if !result.Degraded || power.onCalls != 1 {
		t.Fatalf("result=%+v off=%d on=%d", result, power.offCalls, power.onCalls)
	}
}

func TestConvergePostSwitchUsesCompositeReloadWithDefaultSlotWhenSlotUnknown(t *testing.T) {
	readiness := &postSwitchReadinessStub{readiness: []qmimanager.UIMReadiness{
		{
			TransportReady: true,
			ControlReady:   true,
			CardPresent:    true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			ICCID:          "old",
			Reason:         qmimanager.UIMReadinessReady,
		},
		{
			TransportReady: true,
			ControlReady:   true,
			CardPresent:    true,
			UIMReady:       true,
			SIMStatus:      qmi.SIMReady,
			ICCID:          "target",
			Reason:         qmimanager.UIMReadinessReady,
		},
	}}
	reloader := &postSwitchCompositeReloadStub{}

	result := convergePostSwitch(context.Background(), readiness, reloader, postSwitchConvergenceOptions{
		TargetICCID:         "target",
		IdentityAttempts:    2,
		ReloadAfterAttempts: 1,
	})
	if !result.Ready {
		t.Fatalf("result=%+v want ready", result)
	}
	if reloader.slot != 1 {
		t.Fatalf("reload slot=%d want default slot 1", reloader.slot)
	}
}

func TestConvergePostSwitchSlotUnknownFallsBackToSlot1PowerCycle(t *testing.T) {
	backend := &postSwitchRunBackendStub{
		readiness: []qmimanager.UIMReadiness{
			{TransportReady: true, ControlReady: true, UIMReady: true, CardPresent: true, ICCID: "11111", ActiveSlot: 0, SlotKnown: false, Reason: "ready"},
			{TransportReady: true, ControlReady: true, UIMReady: true, CardPresent: true, ICCID: "11111", ActiveSlot: 0, SlotKnown: false, Reason: "ready"},
			{TransportReady: true, ControlReady: true, UIMReady: true, CardPresent: true, ICCID: "11111", ActiveSlot: 0, SlotKnown: false, Reason: "ready"},
			{TransportReady: true, ControlReady: true, UIMReady: true, CardPresent: true, ICCID: "11111", ActiveSlot: 0, SlotKnown: false, Reason: "ready"},
			{TransportReady: true, ControlReady: true, UIMReady: true, CardPresent: true, ICCID: "11111", ActiveSlot: 0, SlotKnown: false, Reason: "ready"},
			{TransportReady: true, ControlReady: true, UIMReady: true, CardPresent: true, ICCID: "11111", ActiveSlot: 0, SlotKnown: false, Reason: "ready"}, // attempt 6: triggers fallback
			{TransportReady: true, ControlReady: true, UIMReady: true, CardPresent: true, ICCID: "22222", ActiveSlot: 0, SlotKnown: false, Reason: "ready"},
		},
	}
	res := convergePostSwitch(context.Background(), backend, backend, postSwitchConvergenceOptions{
		TargetICCID:         "22222",
		IdentityAttempts:    10,
		ReloadAfterAttempts: 6,
	})
	if !res.Ready {
		t.Fatalf("expected ready, got degraded: %s", res.Reason)
	}
	if backend.offCalls != 1 {
		t.Fatalf("expected 1 power off call, got %v", backend.offCalls)
	}
	if backend.onCalls != 1 {
		t.Fatalf("expected 1 power on call, got %v", backend.onCalls)
	}
}
