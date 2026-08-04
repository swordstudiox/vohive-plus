package device

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestRunQMIStartCoreAttemptReturnsAfterStartupBudget(t *testing.T) {
	startCore := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	start := time.Now()
	result := runQMIStartCoreAttempt(context.Background(), startCore, 20*time.Millisecond)

	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", result.err)
	}
	if !result.retry {
		t.Fatal("retry = false, want true for startup budget timeout")
	}
	if result.abort {
		t.Fatal("abort = true, want false for startup budget timeout")
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("elapsed = %v, want bounded by startup budget", elapsed)
	}
}

func TestRunQMIStartCoreAttemptAbortsKnownFatalStartupError(t *testing.T) {
	result := runQMIStartCoreAttempt(context.Background(), func(context.Context) error {
		return errors.New("open /dev/cdc-wdm9: no such file or directory")
	}, time.Second)

	if !result.abort {
		t.Fatal("abort = false, want true for missing QMI control device")
	}
	if result.retry {
		t.Fatal("retry = true, want false for missing QMI control device")
	}
}

func TestRunQMIStartCoreRetryAttemptIsBounded(t *testing.T) {
	startCore := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	start := time.Now()
	err := runQMIStartCoreRetryAttempt(context.Background(), startCore, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("elapsed = %v, want bounded by retry budget", elapsed)
	}
}

func TestStartAllReturnsBeforeQMIDiscoveryCompletes(t *testing.T) {
	origDiscover := discoverQMIDevicesFn
	releaseDiscover := make(chan struct{})
	discoverEntered := make(chan struct{})
	discoverQMIDevicesFn = func() ([]QMIDevice, error) {
		close(discoverEntered)
		<-releaseDiscover
		return nil, nil
	}
	t.Cleanup(func() {
		close(releaseDiscover)
		discoverQMIDevicesFn = origDiscover
	})

	p := NewPool(&config.Config{Devices: []config.DeviceConfig{{
		ID:            "dev-qmi",
		ModemIMEI:     "860000000000001",
		ControlDevice: "/dev/cdc-wdm-test",
		Interface:     "wwan-test",
		DeviceBackend: "qmi",
	}}})
	t.Cleanup(func() { _ = p.Shutdown() })

	done := make(chan error, 1)
	go func() {
		done <- p.StartAll()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("StartAll() error = %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("StartAll() blocked on QMI discovery; startup must not wait for device bootstrap")
	}

	select {
	case <-discoverEntered:
	case <-time.After(time.Second):
		t.Fatal("background QMI discovery did not start")
	}
}
