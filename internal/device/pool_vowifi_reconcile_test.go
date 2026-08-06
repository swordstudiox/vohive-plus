package device

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/carrier"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/cardpolicy"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
)

func newDesiredVoWiFiTestPool(t *testing.T, deviceID string, enabled bool, imsi string) *Pool {
	t.Helper()
	p := NewPool(&config.Config{})
	w := &Worker{
		ID:      deviceID,
		Config:  config.DeviceConfig{ID: deviceID, VoWiFiEnabled: enabled},
		Backend: &vowifiLockBackendStub{mode: backend.BackendQMI, imsi: imsi, imei: "861234567890123"},
		Pool:    p,
		stop:    make(chan struct{}),
	}
	w.state.Identity.IMSI = imsi
	w.state.Identity.Ready = imsi != ""
	if imsi != "" {
		w.state.Identity.ICCID = "iccid-" + deviceID
		p.SetPolicyResolver(&stubPolicyResolver{
			pol: cardpolicy.Policy{ICCID: w.state.Identity.ICCID, VoWiFiEnabled: enabled},
		})
	}
	w.state.Meta.Healthy = true

	p.mu.Lock()
	p.workers[deviceID] = w
	p.mu.Unlock()
	return p
}

func waitForRecoverCommand(t *testing.T, ch <-chan vowifihost.LifecycleCommand) vowifihost.LifecycleCommand {
	t.Helper()
	select {
	case cmd := <-ch:
		return cmd
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recover command")
	}
	return vowifihost.LifecycleCommand{}
}

func assertNoRecoverCommand(t *testing.T, ch <-chan vowifihost.LifecycleCommand) {
	t.Helper()
	select {
	case cmd := <-ch:
		t.Fatalf("unexpected recover command: %+v", cmd)
	case <-time.After(120 * time.Millisecond):
	}
}

func waitUntilDesiredVoWiFiTest(t *testing.T, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ok() {
		return
	}
	t.Fatal("condition was not met before timeout")
}

type desiredVoWiFiMapPolicyResolver struct {
	policies map[string]cardpolicy.Policy
}

func (r desiredVoWiFiMapPolicyResolver) Resolve(iccid string) (cardpolicy.Policy, error) {
	if pol, ok := r.policies[iccid]; ok {
		return pol, nil
	}
	return cardpolicy.Policy{ICCID: iccid}, nil
}

func TestDesiredVoWiFiInactiveSchedulesRecover(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "001010000000001")
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.reconcileDesiredVoWiFiOnce(time.Now())

	cmd := waitForRecoverCommand(t, commands)
	if cmd.Kind != vowifihost.LifecycleCommandRecover {
		t.Fatalf("kind = %s, want recover", cmd.Kind.String())
	}
	if cmd.DeviceID != "dev-1" {
		t.Fatalf("deviceID = %q, want dev-1", cmd.DeviceID)
	}
	if cmd.Reason != "desired_reconcile" {
		t.Fatalf("reason = %q, want desired_reconcile", cmd.Reason)
	}
}

func TestDesiredVoWiFiRecoverSkipsWhenSIMIdentityNotReady(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "")
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.reconcileDesiredVoWiFiOnce(time.Now())

	assertNoRecoverCommand(t, commands)
}

func TestInitialDesiredVoWiFiStartsDoNotBlockBehindFirstDevice(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-a", true, "001010000000001")
	w := &Worker{
		ID:      "dev-b",
		Config:  config.DeviceConfig{ID: "dev-b", VoWiFiEnabled: true},
		Backend: &vowifiLockBackendStub{mode: backend.BackendQMI, imsi: "001010000000002", imei: "861234567890124"},
		Pool:    p,
		stop:    make(chan struct{}),
	}
	w.state.Identity.IMSI = "001010000000002"
	w.state.Identity.ICCID = "iccid-dev-b"
	w.state.Identity.Ready = true
	w.state.Meta.Healthy = true
	p.mu.Lock()
	p.workers["dev-b"] = w
	p.mu.Unlock()

	commands := make(chan vowifihost.LifecycleCommand, 2)
	release := make(chan struct{})
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		<-release
		return nil
	}

	p.scheduleInitialDesiredVoWiFiStarts(time.Now())

	seen := map[string]bool{}
	deadline := time.After(time.Second)
	for len(seen) < 2 {
		select {
		case cmd := <-commands:
			if cmd.Kind != vowifihost.LifecycleCommandRecover {
				t.Fatalf("kind = %s, want recover", cmd.Kind.String())
			}
			if cmd.Reason != vowifiInitialAutoStartReason {
				t.Fatalf("reason = %q, want %q", cmd.Reason, vowifiInitialAutoStartReason)
			}
			seen[cmd.DeviceID] = true
		case <-deadline:
			t.Fatalf("timed out waiting for both initial starts, saw %v", seen)
		}
	}
	close(release)
}

func TestDesiredVoWiFiDoesNotRecoverWhenRuntimeHostInstanceActive(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "001010000000001")
	p.voWiFiRuntimeStore().SetInstance("dev-1", &runtimehost.Instance{})
	t.Cleanup(func() { p.voWiFiRuntimeStore().DeleteInstance("dev-1", nil) })
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.reconcileDesiredVoWiFiOnce(time.Now())

	assertNoRecoverCommand(t, commands)
}

func TestScheduleDesiredVoWiFiRecoverSkipsWhenRuntimeHostInstanceActive(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "001010000000001")
	p.voWiFiRuntimeStore().SetInstance("dev-1", &runtimehost.Instance{})
	t.Cleanup(func() { p.voWiFiRuntimeStore().DeleteInstance("dev-1", nil) })

	if scheduled := p.scheduleDesiredVoWiFiRecover("dev-1", "test", time.Now()); scheduled {
		t.Fatal("scheduleDesiredVoWiFiRecover() = true with active runtimehost instance, want false")
	}
}

func TestDesiredVoWiFiRecoverBacksOffAfterFailure(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "001010000000001")
	commands := make(chan vowifihost.LifecycleCommand, 2)
	release := make(chan struct{})
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		<-release
		return errors.New("network down")
	}

	now := time.Now()
	p.reconcileDesiredVoWiFiOnce(now)
	_ = waitForRecoverCommand(t, commands)
	close(release)

	waitUntilDesiredVoWiFiTest(t, time.Second, func() bool {
		st, ok := p.voWiFiHost().DesiredRecoverState("dev-1")
		return ok && !st.InFlight && st.Attempt == 1 && !st.NextAt.Before(now.Add(30*time.Second))
	})

	p.reconcileDesiredVoWiFiOnce(now.Add(29 * time.Second))
	assertNoRecoverCommand(t, commands)

	p.reconcileDesiredVoWiFiOnce(now.Add(31 * time.Second))
	_ = waitForRecoverCommand(t, commands)
}

func TestDesiredVoWiFiRecoverResetsAfterSuccess(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "001010000000001")
	now := time.Now().Add(-time.Minute)
	if !p.voWiFiHost().BeginDesiredRecover("dev-1", now) {
		t.Fatal("expected recover state setup to begin")
	}
	p.voWiFiHost().MarkDesiredRecoverFailed("dev-1", now, errors.New("network down"))

	p.markDesiredVoWiFiRecoverResult("dev-1", nil)

	if p.voWiFiHost().HasDesiredRecoverState("dev-1") {
		t.Fatal("recover state should be cleared after success")
	}
}

func TestDesiredVoWiFiPolicyBlockedDoesNotRetryForever(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "460001234567890")
	w := p.GetWorker("dev-1")
	w.cacheMu.Lock()
	w.state.Identity.NativeMCC = "460"
	w.state.Identity.NativeMNC = "00"
	w.cacheMu.Unlock()
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.reconcileDesiredVoWiFiOnce(time.Now())

	assertNoRecoverCommand(t, commands)
	if p.voWiFiHost().HasDesiredRecoverState("dev-1") {
		t.Fatal("policy-blocked device should not keep recover state")
	}

	blockErr := carrier.NewVoWiFiBlockedMCCError("460")
	p.markDesiredVoWiFiRecoverResult("dev-1", blockErr)
	if p.voWiFiHost().HasDesiredRecoverState("dev-1") {
		t.Fatal("policy-blocked failure should clear recover state")
	}
}

func TestDesiredVoWiFiRecoverUsesCachedHomeMCCMNCForPolicy(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "460001234567890")
	w := p.GetWorker("dev-1")
	w.cacheMu.Lock()
	w.state.Identity.NativeMCC = "515"
	w.state.Identity.NativeMNC = "66"
	w.cacheMu.Unlock()
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.reconcileDesiredVoWiFiOnce(time.Now())

	cmd := waitForRecoverCommand(t, commands)
	if cmd.Kind != vowifihost.LifecycleCommandRecover {
		t.Fatalf("kind = %s, want recover", cmd.Kind.String())
	}
}

func TestDesiredVoWiFiDoesNotRecoverWhenDisabled(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", false, "001010000000001")
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.reconcileDesiredVoWiFiOnce(time.Now())

	assertNoRecoverCommand(t, commands)
}

func TestDesiredVoWiFiDoesNotRecoverWhenCurrentCardPolicyDisabled(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "001010000000001")
	defer p.cancel()
	p.SetPolicyResolver(&stubPolicyResolver{
		pol: cardpolicy.Policy{ICCID: "iccid-new", VoWiFiEnabled: false},
	})
	w := p.GetWorker("dev-1")
	w.cacheMu.Lock()
	w.state.Identity.ICCID = "iccid-new"
	w.cacheMu.Unlock()
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.reconcileDesiredVoWiFiOnce(time.Now())

	assertNoRecoverCommand(t, commands)
}

func TestDesiredVoWiFiDoesNotRecoverWithUnconfirmedTargetICCID(t *testing.T) {
	p := newDesiredVoWiFiTestPool(t, "dev-1", true, "001010000000001")
	defer p.cancel()
	p.SetPolicyResolver(desiredVoWiFiMapPolicyResolver{policies: map[string]cardpolicy.Policy{
		"iccid-current": {ICCID: "iccid-current", VoWiFiEnabled: false},
		"iccid-target":  {ICCID: "iccid-target", VoWiFiEnabled: true},
	}})
	w := p.GetWorker("dev-1")
	w.cacheMu.Lock()
	w.state.Identity.ICCID = "iccid-current"
	w.state.Identity.TargetICCID = "iccid-target"
	w.state.Identity.Phase = simIdentityPhaseDegraded
	w.cacheMu.Unlock()
	commands := make(chan vowifihost.LifecycleCommand, 1)
	p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
		commands <- cmd
		return nil
	}

	p.reconcileDesiredVoWiFiOnce(time.Now())

	assertNoRecoverCommand(t, commands)
}

func TestDesiredVoWiFiDoesNotRecoverDuringSwitchOrRebuild(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Pool)
	}{
		{
			name: "switching",
			setup: func(p *Pool) {
				p.switchMu.Lock()
				p.switchingDevices["dev-1"] = true
				p.switchMu.Unlock()
			},
		},
		{
			name: "rebuilding",
			setup: func(p *Pool) {
				p.mu.Lock()
				p.rebuilding["dev-1"] = true
				p.mu.Unlock()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newDesiredVoWiFiTestPool(t, "dev-1", true, "001010000000001")
			tt.setup(p)
			commands := make(chan vowifihost.LifecycleCommand, 1)
			p.voWiFiHost().LifecycleControllerForTest().TestRun = func(ctx context.Context, cmd vowifihost.LifecycleCommand) error {
				commands <- cmd
				return nil
			}

			p.reconcileDesiredVoWiFiOnce(time.Now())

			assertNoRecoverCommand(t, commands)
		})
	}
}

func TestVoWiFiDesiredRecoverDelayCapsAtTwoMinutes(t *testing.T) {
	got := []time.Duration{
		vowifihost.DesiredRecoverDelay(0),
		vowifihost.DesiredRecoverDelay(1),
		vowifihost.DesiredRecoverDelay(2),
		vowifihost.DesiredRecoverDelay(10),
	}
	want := []time.Duration{
		30 * time.Second,
		time.Minute,
		2 * time.Minute,
		2 * time.Minute,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
}
