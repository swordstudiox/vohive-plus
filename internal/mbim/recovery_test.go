package mbimcore

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func TestOnRecoveryExhaustedDispatch(t *testing.T) {
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	got := make(chan struct {
		reason string
		err    error
	}, 1)
	m.OnRecoveryExhausted(func(reason string, err error) {
		got <- struct {
			reason string
			err    error
		}{reason, err}
	})

	want := errors.New("boom")
	m.dispatchRecoveryExhausted("probe_failed", want)

	select {
	case ev := <-got:
		if ev.reason != "probe_failed" || ev.err != want {
			t.Fatalf("got (%q,%v), want (probe_failed, boom)", ev.reason, ev.err)
		}
	default:
		t.Fatal("handler not invoked")
	}
}

// sequenceDial returns a dial func that vends the given transports in order,
// one per Open call.
func sequenceDial(transports ...mbim.Transport) (func(string, string) (mbim.Transport, error), *int32) {
	var idx int32
	fn := func(_, _ string) (mbim.Transport, error) {
		i := atomic.AddInt32(&idx, 1) - 1
		if int(i) >= len(transports) {
			return nil, fmt.Errorf("sequenceDial exhausted at call %d", i+1)
		}
		return transports[i], nil
	}
	return fn, &idx
}

func TestRunRecoverySucceedsWhenReopenHealthy(t *testing.T) {
	bad := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(1000))
	good := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(0))
	dialFn, _ := sequenceDial(good)
	m := &Manager{
		controlDevice:       "/dev/cdc-wdm0",
		transportMode:       "direct",
		healthProbeInterval: time.Hour,
		healthProbeTimeout:  20 * time.Millisecond,
		dial:                dialFn,
	}
	exhausted := make(chan struct{}, 1)
	m.OnRecoveryExhausted(func(string, error) { exhausted <- struct{}{} })
	if err := m.openWithTransport(context.Background(), bad); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	m.runRecovery("test")

	select {
	case <-exhausted:
		t.Fatal("recovery should have succeeded, but exhausted fired")
	default:
	}
}

func TestRunRecoveryExhaustsWhenReopenKeepsFailing(t *testing.T) {
	bad := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(1000))
	bad2 := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(1000))
	bad3 := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(1000))
	bad4 := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(1000))
	dialFn, _ := sequenceDial(bad2, bad3, bad4)
	m := &Manager{
		controlDevice:       "/dev/cdc-wdm0",
		transportMode:       "direct",
		healthProbeInterval: time.Hour,
		healthProbeTimeout:  20 * time.Millisecond,
		reopenBackoff:       5 * time.Millisecond,
		dial:                dialFn,
	}
	exhausted := make(chan struct {
		reason string
	}, 1)
	m.OnRecoveryExhausted(func(reason string, _ error) { exhausted <- struct{ reason string }{reason} })
	if err := m.openWithTransport(context.Background(), bad); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	m.runRecovery("still_broken")

	select {
	case ev := <-exhausted:
		if ev.reason != "still_broken" {
			t.Fatalf("reason = %q, want still_broken", ev.reason)
		}
	default:
		t.Fatal("expected exhausted to fire after max reopen attempts")
	}
}

func TestTriggerReopenIsSingleFlight(t *testing.T) {
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	var running atomic.Int32
	var maxObserved atomic.Int32
	release := make(chan struct{})
	m.runRecoveryHook = func(string) {
		n := running.Add(1)
		if n > maxObserved.Load() {
			maxObserved.Store(n)
		}
		<-release
		running.Add(-1)
	}
	for i := 0; i < 5; i++ {
		m.triggerReopen("x")
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	time.Sleep(50 * time.Millisecond)
	if maxObserved.Load() > 1 {
		t.Fatalf("max concurrent recoveries = %d, want 1", maxObserved.Load())
	}
}
