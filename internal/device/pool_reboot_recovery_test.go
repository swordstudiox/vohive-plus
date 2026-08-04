package device

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func TestModemRebootRecoveryDefaults(t *testing.T) {
	opts := defaultModemRebootRecoveryOptions("dev-1", "manual_reboot")
	if opts.deviceID != "dev-1" || opts.reason != "manual_reboot" {
		t.Fatalf("unexpected options: %#v", opts)
	}
	wantDelays := []time.Duration{0, time.Second, 3 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second}
	if !reflect.DeepEqual(opts.delays, wantDelays) {
		t.Fatalf("delays = %v, want %v", opts.delays, wantDelays)
	}
	if !opts.removeBeforeScan {
		t.Fatal("manual reboot recovery should remove stale worker before scanning")
	}
}

func TestDefaultModemRebootRecoveryStartsWithImmediateAttempt(t *testing.T) {
	opts := defaultModemRebootRecoveryOptions("dev-qmi", "qmi_transport_failed")
	if len(opts.delays) == 0 {
		t.Fatal("delays empty")
	}
	if opts.delays[0] != 0 {
		t.Fatalf("first delay = %s, want 0", opts.delays[0])
	}
}

func TestModemRebootRecoverySuppressesDuplicateDeviceRecovery(t *testing.T) {
	p := NewPool(&config.Config{})
	if !p.beginModemRebootRecovery("dev-1") {
		t.Fatal("first beginModemRebootRecovery = false, want true")
	}
	if p.beginModemRebootRecovery("dev-1") {
		t.Fatal("second beginModemRebootRecovery = true, want false")
	}
	p.finishModemRebootRecovery("dev-1")
	if !p.beginModemRebootRecovery("dev-1") {
		t.Fatal("begin after finish = false, want true")
	}
}

func TestModemRebootRecoveryWakeSignalsActiveRecovery(t *testing.T) {
	p := NewPool(&config.Config{})
	if !p.beginModemRebootRecovery("dev-1") {
		t.Fatal("beginModemRebootRecovery = false, want true")
	}
	defer p.finishModemRebootRecovery("dev-1")

	done := make(chan struct{})
	go func() {
		p.waitModemRebootRecoveryTrigger("dev-1", time.Hour)
		close(done)
	}()

	if woke := p.WakeModemRebootRecoveries("udev_add"); woke != 1 {
		t.Fatalf("WakeModemRebootRecoveries() = %d, want 1", woke)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waitModemRebootRecoveryTrigger did not wake after udev signal")
	}
}

func TestQMIRecoveryControlPathRequiresStableControlDevice(t *testing.T) {
	origStat := qmiControlStatFn
	t.Cleanup(func() { qmiControlStatFn = origStat })

	calls := 0
	qmiControlStatFn = func(name string) (os.FileInfo, error) {
		calls++
		if name != "/dev/cdc-wdm0" {
			t.Fatalf("stat path = %q, want /dev/cdc-wdm0", name)
		}
		if calls == 1 {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}

	cfg := config.DeviceConfig{
		ID:            "dev-qmi",
		DeviceBackend: "qmi",
		ControlDevice: "/dev/cdc-wdm0",
		Interface:     "wwan0",
	}

	if qmiRecoveryControlPathStable(cfg, 0) {
		t.Fatal("qmiRecoveryControlPathStable() = true after second stat failed, want false")
	}
}

func TestQMIRecoveryScanGateAllowsLiveIMEIAttachmentWhenConfiguredPathStale(t *testing.T) {
	cfg := config.DeviceConfig{
		ID:            "dev-qmi",
		DeviceBackend: "qmi",
		ModemIMEI:     "867383058993207",
		ControlDevice: "/dev/cdc-wdm2",
		Interface:     "wwan1",
	}
	live := QMIDevice{
		ControlPath:  "/dev/cdc-wdm0",
		NetInterface: "wwan0",
		USBPath:      "/sys/bus/usb/devices/1-9",
	}
	decision := qmiRecoveryScanGate(cfg, []qmiRecoveryLiveCandidate{{
		Device: live,
		IMEI:   "867383058993207",
	}}, true)

	if !decision.Ready {
		t.Fatalf("Ready = false, want true, reason=%s", decision.Reason)
	}
	if decision.Attachment.ControlPath != "/dev/cdc-wdm0" {
		t.Fatalf("Attachment.ControlPath = %q, want /dev/cdc-wdm0", decision.Attachment.ControlPath)
	}
}

func TestQMIRecoveryScanGateMatchesNormalizedLiveIMEI(t *testing.T) {
	cfg := config.DeviceConfig{
		ID:            "dev-qmi",
		DeviceBackend: backend.BackendQMI,
		ControlDevice: "/dev/cdc-wdm0",
		Interface:     "wwan0",
		ModemIMEI:     "864388041069422",
	}
	live := []qmiRecoveryLiveCandidate{{
		Device: QMIDevice{ControlPath: "/dev/cdc-wdm0", NetInterface: "wwan0"},
		IMEI:   "8643880410694201",
	}}

	decision := qmiRecoveryScanGate(cfg, live, true)
	if !decision.Ready || decision.Reason != "live_imei_match" {
		t.Fatalf("decision = %+v, want Ready live_imei_match", decision)
	}
}

func TestModemRebootRecoveryDoesNotBlockScanOnStaleConfiguredControlPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("devices:\n- id: dev-qmi\n  device_backend: qmi\n  modem_imei: \"867383058993207\"\n  control_device: /dev/cdc-wdm2\n  interface: wwan1\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.InitGlobalManager(configPath); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}

	origStat := qmiControlStatFn
	qmiControlStatFn = func(path string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { qmiControlStatFn = origStat })

	origDiscover := discoverQMIDevicesFn
	discoverQMIDevicesFn = func() ([]QMIDevice, error) {
		return []QMIDevice{{ControlPath: "/dev/cdc-wdm0", NetInterface: "wwan0"}}, nil
	}
	t.Cleanup(func() { discoverQMIDevicesFn = origDiscover })

	origResolve := resolveDiscoveredQMIDeviceFn
	resolveDiscoveredQMIDeviceFn = func(dev QMIDevice, timeout time.Duration, allowIMEIProbe bool) (QMIDevice, string) {
		return dev, "867383058993207"
	}
	t.Cleanup(func() { resolveDiscoveredQMIDeviceFn = origResolve })

	p := NewPool(&config.Config{})
	called := false
	p.rescanAndReconnectForTest = func() error {
		called = true
		return nil
	}

	p.runModemRebootRecovery(modemRebootRecoveryOptions{
		deviceID:         "dev-qmi",
		reason:           "qmi_transport_failed",
		delays:           []time.Duration{0},
		removeBeforeScan: false,
		restoreVoWiFi:    false,
	})

	if !called {
		t.Fatal("RescanAndReconnect was not called despite live IMEI attachment")
	}
}

func TestQMIStartCoreFailureClassifiesTransientUSBErrors(t *testing.T) {
	transient := []string{
		"failed to open QMI device /dev/cdc-wdm0: open /dev/cdc-wdm0: no such file or directory",
		"write failed: write /dev/cdc-wdm0: no such device",
		"QMI: initial sync failed: connection closed",
		"QMI: read failed: EOF",
	}
	for _, msg := range transient {
		if !qmiStartCoreFailureShouldAbortWorker(msg) {
			t.Fatalf("qmiStartCoreFailureShouldAbortWorker(%q) = false, want true", msg)
		}
	}

	if qmiStartCoreFailureShouldAbortWorker("UIM service not supported") {
		t.Fatal("qmiStartCoreFailureShouldAbortWorker(non-transient) = true, want false")
	}
}

func TestModemRebootRecoveryMarkerDoesNotBlockWorkerRebuild(t *testing.T) {
	p := NewPool(&config.Config{})
	if !p.beginModemRebootRecovery("dev-1") {
		t.Fatal("beginModemRebootRecovery = false, want true")
	}
	p.mu.RLock()
	blocked := p.rebuilding["dev-1"]
	p.mu.RUnlock()
	if blocked {
		t.Fatal("modem reboot recovery must not set p.rebuilding; AddWorkerFromConfig needs that slot during recovery")
	}
}

func TestManualModemRebootRecoveryEvictsHalfReadyWorkerAfterIdentityFailure(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("devices: []\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.InitGlobalManager(configPath); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}

	origDiscover := discoverQMIDevicesFn
	discoverQMIDevicesFn = func() ([]QMIDevice, error) { return nil, nil }
	t.Cleanup(func() { discoverQMIDevicesFn = origDiscover })

	p := NewPool(&config.Config{})
	be := &workerStartupIdentityBackendStub{}
	be.workerPhoneBackendStub.workerStatusBackendStub.mode = backend.BackendQMI
	be.workerPhoneBackendStub.workerStatusBackendStub.opMode = backend.ModeOnline
	w := &Worker{
		ID:      "dev-qmi",
		Config:  config.DeviceConfig{ID: "dev-qmi", DeviceBackend: "qmi"},
		Backend: be,
		Pool:    p,
		stop:    make(chan struct{}),
	}
	w.state.Identity.IMEI = "861716071104530"

	p.mu.Lock()
	p.workers[w.ID] = w
	p.mu.Unlock()

	p.runModemRebootRecovery(modemRebootRecoveryOptions{
		deviceID:         w.ID,
		reason:           "manual_reboot",
		delays:           []time.Duration{0},
		removeBeforeScan: false,
		restoreVoWiFi:    false,
	})

	if got := p.GetWorker(w.ID); got != nil {
		t.Fatal("manual reboot recovery left half-ready worker in pool after live_identity_empty")
	}
}

func TestModemRebootRecoveryKeepsControlReadyWorkerOnTransientIdentityFailure(t *testing.T) {
	opts := modemRebootRecoveryOptions{
		deviceID:         "dev-qmi",
		reason:           "qmi_transport_failed",
		removeBeforeScan: true,
	}
	err := fmt.Errorf("refresh_identity: identity not readable before deadline: context deadline exceeded")
	worker := &Worker{
		ID:     "dev-qmi",
		Config: config.DeviceConfig{ID: "dev-qmi", DeviceBackend: "qmi", ControlDevice: "/dev/cdc-wdm0"},
		stop:   make(chan struct{}),
	}
	worker.RecordWatchdogEvent(WatchdogEvent{
		Layer:     HealthLayerQMI,
		State:     HealthStateHealthy,
		EventType: "qmi_control_ready",
		Reason:    "test",
	})

	if modemRebootRecoveryShouldRebuildAfterReadinessFailureForWorker(opts, worker, err) {
		t.Fatal("transient identity failure should not rebuild a control-ready QMI worker")
	}
}

func TestModemRebootRecoveryKeepsControlReadyWorkerWhenIdentityEmpty(t *testing.T) {
	p := NewPool(&config.Config{})
	defer p.cancel()
	w := &Worker{ID: "dev-1"}
	w.RecordWatchdogEvent(WatchdogEvent{
		Layer: HealthLayerQMI,
		State: HealthStateHealthy,
	})

	err := errors.New("refresh_identity: live_identity_empty")
	opts := defaultModemRebootRecoveryOptions("dev-1", qmiTransportFailureRecoveryReason)

	if modemRebootRecoveryShouldRebuildAfterReadinessFailureForWorker(opts, w, err) {
		t.Fatal("control-ready worker with identity convergence should not be removed")
	}
}

func TestModemRebootRecoveryKeepsControlReadyWorkerOnTransportError(t *testing.T) {
	w := &Worker{ID: "dev-1"}
	w.RecordWatchdogEvent(WatchdogEvent{
		Layer: HealthLayerQMI,
		State: HealthStateHealthy,
	})

	err := errors.New("refresh_identity: QMI: read failed: EOF")
	opts := defaultModemRebootRecoveryOptions("dev-1", qmiTransportFailureRecoveryReason)

	if modemRebootRecoveryShouldRebuildAfterReadinessFailureForWorker(opts, w, err) {
		t.Fatal("transport-looking identity refresh failure should not rebuild a control-ready QMI worker")
	}
}

func TestModemRebootRecoveryRebuildsOnTransportDownWhenControlNotReady(t *testing.T) {
	w := &Worker{ID: "dev-1"}
	w.RecordWatchdogEvent(WatchdogEvent{
		Layer: HealthLayerQMI,
		State: HealthStateRecovering,
	})
	err := errors.New("refresh_identity: write failed: write unix @->@qmi-proxy: write: broken pipe")
	if !modemRebootRecoveryShouldRebuildAfterTransportDown(w, err) {
		t.Fatal("dead transport with non-ready control should trigger rebuild")
	}
}

func TestModemRebootRecoveryKeepsControlReadyWorkerOnTransientTransportBlip(t *testing.T) {
	w := &Worker{ID: "dev-1"}
	w.RecordWatchdogEvent(WatchdogEvent{
		Layer: HealthLayerQMI,
		State: HealthStateHealthy,
	})
	err := errors.New("refresh_identity: QMI: read failed: EOF")
	if modemRebootRecoveryShouldRebuildAfterTransportDown(w, err) {
		t.Fatal("transient transport blip on a control-ready worker must not rebuild")
	}
}

func TestModemRebootRecoveryTransportDownIgnoresIdentityEmpty(t *testing.T) {
	w := &Worker{ID: "dev-1"}
	w.RecordWatchdogEvent(WatchdogEvent{Layer: HealthLayerQMI, State: HealthStateRecovering})
	err := errors.New("refresh_identity: live_identity_empty")
	if modemRebootRecoveryShouldRebuildAfterTransportDown(w, err) {
		t.Fatal("identity-empty (non-transport) error must not be treated as transport down")
	}
}

func TestModemRebootRecoveryStartsIdentityConvergenceForControlReadyWorker(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("devices: []\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.InitGlobalManager(configPath); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}

	p := NewPool(&config.Config{})
	defer p.cancel()
	be := &workerStartupIdentityBackendStub{}
	be.workerPhoneBackendStub.workerStatusBackendStub.mode = backend.BackendQMI
	be.workerPhoneBackendStub.workerStatusBackendStub.opMode = backend.ModeOnline
	w := &Worker{
		ID:      "dev-qmi",
		Config:  config.DeviceConfig{ID: "dev-qmi", DeviceBackend: "qmi", ControlDevice: "/dev/cdc-wdm0"},
		Backend: be,
		Pool:    p,
		stop:    make(chan struct{}),
	}
	w.RecordWatchdogEvent(WatchdogEvent{
		Layer:     HealthLayerQMI,
		State:     HealthStateHealthy,
		EventType: "qmi_control_ready",
		Reason:    "test",
	})

	p.mu.Lock()
	p.workers[w.ID] = w
	p.mu.Unlock()
	defer close(w.stop)

	p.runModemRebootRecovery(modemRebootRecoveryOptions{
		deviceID:         w.ID,
		reason:           "qmi_transport_failed",
		delays:           []time.Duration{0},
		removeBeforeScan: false,
		restoreVoWiFi:    false,
	})

	snapshot := w.HealthSnapshot()
	if snapshot.EventType != "qmi_identity_converging" {
		t.Fatalf("event=%q want qmi_identity_converging", snapshot.EventType)
	}
	if snapshot.State == HealthStateFailed {
		t.Fatal("control-ready identity delay should not mark recovery exhausted")
	}
}

func TestMarkQMIControlRecoveredFinishesLifecycleAndHealth(t *testing.T) {
	p := NewPool(&config.Config{})
	w := &Worker{
		ID:     "dev-qmi",
		Config: config.DeviceConfig{ID: "dev-qmi", DeviceBackend: "qmi"},
		stop:   make(chan struct{}),
	}
	p.lifecycle.BeginRecovery(w.ID, LifecyclePhaseQMIStarting, "qmi_start_core", qmiLifecycleRecoveryTTL)

	p.markQMIControlRecovered(w, "qmi_start_core")

	if snap := p.lifecycle.GetSnapshot(w.ID); snap.Phase != LifecyclePhaseOnline {
		t.Fatalf("lifecycle phase = %s, want online", snap.Phase)
	}
	health := w.HealthSnapshot()
	if health.State != HealthStateHealthy || health.Layer != HealthLayerQMI {
		t.Fatalf("health = state=%s layer=%s, want healthy qmi", health.State, health.Layer)
	}
}

func TestWorkerATProbeTreatsQMIOnlyWorkerAsOK(t *testing.T) {
	m, err := modem.New(config.DeviceConfig{ID: "dev-qmi", DeviceBackend: "qmi"})
	if err != nil {
		t.Fatalf("modem.New() error = %v", err)
	}
	if !workerATProbeOK(&Worker{ID: "dev-qmi", Modem: m}, 10*time.Millisecond) {
		t.Fatal("workerATProbeOK() = false for QMI-only modem without AT port, want true")
	}
}

func TestWorkerATProbeTreatsPureQMIWorkerWithManualATPortAsOK(t *testing.T) {
	m, err := modem.New(config.DeviceConfig{ID: "dev-qmi", DeviceBackend: "qmi", ATPort: "/dev/ttyUSB6"})
	if err != nil {
		t.Fatalf("modem.New() error = %v", err)
	}
	w := &Worker{
		ID:     "dev-qmi",
		Config: config.DeviceConfig{ID: "dev-qmi", DeviceBackend: "qmi", ATPort: "/dev/ttyUSB6"},
		Modem:  m,
	}
	if !workerATProbeOK(w, 10*time.Millisecond) {
		t.Fatal("workerATProbeOK() = false for pure QMI worker with manual AT port, want true")
	}
}

func TestWorkerATProbeFailsWhenATManagerIsNotRunning(t *testing.T) {
	m, err := modem.New(config.DeviceConfig{ID: "dev-at", DeviceBackend: "at", ATPort: "/dev/ttyUSB6"})
	if err != nil {
		t.Fatalf("modem.New() error = %v", err)
	}
	if workerATProbeOK(&Worker{ID: "dev-at", Modem: m}, 10*time.Millisecond) {
		t.Fatal("workerATProbeOK() = true for stopped AT manager, want false")
	}
}

func TestModemRebootRecoveryRequiresSIMIdentityBeforeSuccess(t *testing.T) {
	p := NewPool(&config.Config{})
	w := &Worker{
		ID:      "dev-at",
		Config:  config.DeviceConfig{ID: "dev-at"},
		Backend: &workerStartupIdentityBackendStub{},
	}
	w.state.Identity.IMEI = "imei-at-1"

	if err := p.refreshModemRebootRecoveredIdentity(w, "manual_reboot"); err == nil {
		t.Fatal("refreshModemRebootRecoveredIdentity() error = nil, want identity-not-ready error")
	}
}

func TestModemRebootRecoveryRejectsStaleIdentityWhenLiveSIMIDsEmpty(t *testing.T) {
	var p *Pool
	w := &Worker{
		ID:     "dev-at",
		Config: config.DeviceConfig{ID: "dev-at"},
		Backend: &workerStartupIdentityBackendStub{
			liveNativeSPN: "Carrier",
		},
	}
	w.state.Identity.IMEI = "imei-at-1"
	w.state.Identity.ICCID = "stale-iccid"
	w.state.Identity.IMSI = "stale-imsi"

	if err := p.refreshModemRebootRecoveredIdentity(w, "manual_reboot"); err == nil {
		t.Fatal("refreshModemRebootRecoveredIdentity() error = nil, want stale identity rejected")
	}
}

func TestModemRebootRecoveryPersistsSIMIdentityBeforeSuccess(t *testing.T) {
	initDevicePhoneNumberTestDB(t)

	p := NewPool(&config.Config{})
	w := &Worker{
		ID:     "dev-at",
		Config: config.DeviceConfig{ID: "dev-at"},
		Backend: &workerStartupIdentityBackendStub{
			liveICCID: "8986001234567890123",
			liveIMSI:  "460011234567890",
		},
	}
	w.state.Identity.IMEI = "imei-at-1"

	if err := p.refreshModemRebootRecoveredIdentity(w, "manual_reboot"); err != nil {
		t.Fatalf("refreshModemRebootRecoveredIdentity() error = %v", err)
	}
	status := w.ProjectDeviceStatus()
	if status.ICCID != "8986001234567890123" || status.IMSI != "460011234567890" {
		t.Fatalf("identity = iccid=%q imsi=%q, want live values", status.ICCID, status.IMSI)
	}

	var sim db.SIMCard
	if err := db.DB.Where("iccid = ?", "8986001234567890123").First(&sim).Error; err != nil {
		t.Fatalf("SIM identity was not persisted: %v", err)
	}
}

func TestManualRebootRecoveryDelaysSkipImmediateRound(t *testing.T) {
	delays := manualRebootRecoveryDelays()
	if len(delays) == 0 {
		t.Fatal("manual reboot delays must not be empty")
	}
	if delays[0] <= 0 {
		t.Fatalf("first manual-reboot scan must wait for the module to drop, got %v", delays[0])
	}
	var total time.Duration
	for _, d := range delays {
		total += d
	}
	if total < 30*time.Second {
		t.Fatalf("manual reboot delays should span >=30s total, got %v", total)
	}
}
