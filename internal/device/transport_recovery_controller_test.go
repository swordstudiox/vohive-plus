package device

import (
	"errors"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestTransportRecoveryControllerSerializesPerDevice(t *testing.T) {
	controller := NewTransportRecoveryController(nil)
	event := TransportRecoveryEvent{
		DeviceID: "dev1",
		Kind:     TransportRecoveryEventRecoveryExhausted,
		Err:      errors.New("QMI: read failed: EOF"),
		At:       time.Now(),
	}

	if !controller.Observe(event) {
		t.Fatal("first Observe() = false, want true")
	}
	if controller.Observe(event) {
		t.Fatal("second Observe() = true, want false while recovery is active")
	}

	controller.Finish("dev1")
	if !controller.Observe(event) {
		t.Fatal("Observe() after Finish = false, want true")
	}
}

func TestTransportRecoveryControllerAllowsDifferentDevices(t *testing.T) {
	controller := NewTransportRecoveryController(nil)
	err := errors.New("QMI: read failed: EOF")

	if !controller.Observe(TransportRecoveryEvent{DeviceID: "dev1", Kind: TransportRecoveryEventRecoveryExhausted, Err: err}) {
		t.Fatal("dev1 Observe() = false, want true")
	}
	if !controller.Observe(TransportRecoveryEvent{DeviceID: "dev2", Kind: TransportRecoveryEventRecoveryExhausted, Err: err}) {
		t.Fatal("dev2 Observe() = false, want true")
	}
}

func TestTransportRecoveryControllerIgnoresStaleWorkerGeneration(t *testing.T) {
	controller := NewTransportRecoveryController(nil)
	controller.SetWorkerGenerationForTest("dev1", 3)

	if controller.Observe(TransportRecoveryEvent{
		DeviceID:         "dev1",
		WorkerGeneration: 2,
		Kind:             TransportRecoveryEventRecoveryExhausted,
		Err:              errors.New("QMI: read failed: EOF"),
	}) {
		t.Fatal("stale generation Observe() = true, want false")
	}
	if !controller.Observe(TransportRecoveryEvent{
		DeviceID:         "dev1",
		WorkerGeneration: 3,
		Kind:             TransportRecoveryEventRecoveryExhausted,
		Err:              errors.New("QMI: read failed: EOF"),
	}) {
		t.Fatal("current generation Observe() = false, want true")
	}
}

func TestTransportRecoveryControllerAcceptsStructuredRecoveryEvents(t *testing.T) {
	controller := NewTransportRecoveryController(nil)

	if !controller.Observe(TransportRecoveryEvent{
		DeviceID: "dev1",
		Kind:     TransportRecoveryEventRecoveryExhausted,
		Err:      errors.New("write failed: write unix @->@qmi-proxy: write: broken pipe"),
	}) {
		t.Fatal("recovery exhausted event Observe() = false, want true")
	}
	controller.Finish("dev1")
	if !controller.Observe(TransportRecoveryEvent{
		DeviceID: "dev1",
		Kind:     TransportRecoveryEventHealthSuspect,
		Err:      errors.New("QMI service operation timeout: NAS GetServingSystem: context deadline exceeded"),
	}) {
		t.Fatal("health threshold event Observe() = false, want true")
	}
}

func TestRemoveWorkerRegistrationIfCurrentKeepsNewWorker(t *testing.T) {
	pool := NewPool(&config.Config{})
	defer pool.cancel()

	oldWorker := &Worker{ID: "dev1", stop: make(chan struct{})}
	newWorker := &Worker{ID: "dev1", stop: make(chan struct{})}

	if err := pool.registerWorkerStarting(oldWorker); err != nil {
		t.Fatalf("register old worker: %v", err)
	}
	pool.mu.Lock()
	pool.workers["dev1"] = newWorker
	pool.mu.Unlock()

	pool.removeWorkerRegistrationIfCurrent(oldWorker)

	if got := pool.GetWorker("dev1"); got != newWorker {
		t.Fatalf("GetWorker() = %#v, want new worker", got)
	}
}

func TestQMIRecoveryActiveNotLeakedWhenModemRebootAlreadyRunning(t *testing.T) {
	pool := NewPool(&config.Config{})
	defer pool.cancel()
	pool.transportRecovery = NewTransportRecoveryController(pool)

	// Simulate an AT disconnect recovery occupying the modemRebootRecovering lock
	pool.beginModemRebootRecovery("dev1")

	pool.scheduleWorkerRecoveryWithTransportEvent("dev1", qmiTransportFailureRecoveryReason, &TransportRecoveryEvent{
		DeviceID: "dev1",
		Kind:     TransportRecoveryEventRecoveryExhausted,
		Source:   "recovery_exhausted:test",
		Err:      errors.New("qmi recovery exhausted"),
	})

	// Wait a brief moment to allow the goroutine to hit the beginModemRebootRecovery check and return
	time.Sleep(50 * time.Millisecond)

	// Verify that transportRecovery controller's active map is NOT occupied by this failed attempt
	pool.transportRecovery.mu.Lock()
	_, exists := pool.transportRecovery.active["dev1"]
	pool.transportRecovery.mu.Unlock()

	if exists {
		t.Fatal("transportRecovery.active leaked when modemRebootRecovery was already running")
	}
}

func TestAllowRebuildSlidingWindow(t *testing.T) {
	c := NewTransportRecoveryController(nil)
	now := time.Now()
	dev := "dev-1"

	for i := 0; i < rebuildMaxInWindow; i++ {
		if !c.allowRebuildAt(dev, now.Add(time.Duration(i)*time.Minute)) {
			t.Fatalf("attempt %d within window should be allowed", i+1)
		}
	}
	if c.allowRebuildAt(dev, now.Add(time.Duration(rebuildMaxInWindow)*time.Minute)) {
		t.Fatalf("attempt %d should be rejected (over window cap)", rebuildMaxInWindow+1)
	}
	if !c.allowRebuildAt(dev, now.Add(rebuildWindow+time.Minute)) {
		t.Fatal("attempt after window should be allowed again")
	}
}

func TestAllowRebuildResetOnGenerationChange(t *testing.T) {
	c := NewTransportRecoveryController(nil)
	now := time.Now()
	dev := "dev-2"
	for i := 0; i < rebuildMaxInWindow; i++ {
		c.allowRebuildAt(dev, now)
	}
	if c.allowRebuildAt(dev, now) {
		t.Fatal("should be capped before generation change")
	}
	c.SetWorkerGeneration(dev, 42)
	if !c.allowRebuildAt(dev, now) {
		t.Fatal("generation change should clear the rebuild window")
	}
}
