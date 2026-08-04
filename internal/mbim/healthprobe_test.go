package mbimcore

import (
	"context"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func TestHealthProbeReportsSuspectAfterTwoConsecutiveFailures(t *testing.T) {
	ft := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(1000))
	m := &Manager{
		controlDevice:       "/dev/cdc-wdm0",
		transportMode:       "direct",
		healthProbeInterval: 30 * time.Millisecond,
		healthProbeTimeout:  20 * time.Millisecond,
	}
	events := make(chan HealthEvent, 8)
	m.OnHealth(func(e HealthEvent) { events <- e })
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	select {
	case e := <-events:
		if e.State != HealthEventSuspect {
			t.Fatalf("State = %v, want Suspect", e.State)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not observe a Suspect health event")
	}

	select {
	case e := <-events:
		if e.State == HealthEventSuspect {
			t.Fatalf("got a second Suspect event %+v before the test ended; Suspect must only fire once until recovery", e)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

func TestHealthProbeRecoversToHealthyAfterSuccess(t *testing.T) {
	ft := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(2))
	m := &Manager{
		controlDevice:       "/dev/cdc-wdm0",
		transportMode:       "direct",
		healthProbeInterval: 30 * time.Millisecond,
		healthProbeTimeout:  20 * time.Millisecond,
	}
	events := make(chan HealthEvent, 8)
	m.OnHealth(func(e HealthEvent) { events <- e })
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	var got []HealthEventState
	deadline := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case e := <-events:
			got = append(got, e.State)
		case <-deadline:
			t.Fatalf("only observed %v before timeout, want [Suspect Healthy]", got)
		}
	}
	if got[0] != HealthEventSuspect || got[1] != HealthEventHealthy {
		t.Fatalf("event sequence = %v, want [Suspect Healthy]", got)
	}
}

func TestHealthProbeGoroutineExitsOnClose(t *testing.T) {
	ft := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(0))
	m := &Manager{
		controlDevice:       "/dev/cdc-wdm0",
		transportMode:       "direct",
		healthProbeInterval: 10 * time.Millisecond,
		healthProbeTimeout:  20 * time.Millisecond,
	}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case <-m.healthLoopExited:
	case <-time.After(time.Second):
		t.Fatal("health probe goroutine did not exit after Close")
	}
}

func TestHealthProbeEscalatesToRecovering(t *testing.T) {
	ft := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(1000))
	reopenCalls := make(chan string, 4)
	m := &Manager{
		controlDevice:       "/dev/cdc-wdm0",
		transportMode:       "direct",
		healthProbeInterval: 20 * time.Millisecond,
		healthProbeTimeout:  15 * time.Millisecond,
		triggerReopenHook:   func(reason string) { reopenCalls <- reason },
	}
	events := make(chan HealthEvent, 16)
	m.OnHealth(func(e HealthEvent) { events <- e })
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	var states []HealthEventState
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-events:
			states = append(states, e.State)
			if e.State == HealthEventRecovering {
				goto sawRecovering
			}
		case <-deadline:
			t.Fatalf("never saw Recovering; states=%v", states)
		}
	}

sawRecovering:
	if states[0] != HealthEventSuspect {
		t.Fatalf("first state = %v, want Suspect", states[0])
	}
	select {
	case <-reopenCalls:
	case <-time.After(time.Second):
		t.Fatal("triggerReopen hook not called when Recovering")
	}
}
