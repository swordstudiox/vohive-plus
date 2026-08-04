package device

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestQMIConvergenceShouldEscalate(t *testing.T) {
	if qmiConvergenceShouldEscalate(2, 3) {
		t.Fatal("streak below limit must not escalate")
	}
	if !qmiConvergenceShouldEscalate(3, 3) {
		t.Fatal("streak reaching limit must escalate")
	}
}

func TestConvergeQMIIdentityEscalatesOnPersistentTransportDown(t *testing.T) {
	origRefresh := convergeIdentityRefreshFn
	origEscalate := convergeEscalateFn
	origInterval := qmiConvergenceRetryInterval
	defer func() {
		convergeIdentityRefreshFn = origRefresh
		convergeEscalateFn = origEscalate
		qmiConvergenceRetryInterval = origInterval
	}()
	qmiConvergenceRetryInterval = time.Millisecond

	convergeIdentityRefreshFn = func(p *Pool, w *Worker, reason string) error {
		return errors.New("refresh_identity: write failed: write unix @->@qmi-proxy: write: broken pipe")
	}
	var mu sync.Mutex
	var escalations []string
	convergeEscalateFn = func(p *Pool, w *Worker, reason string, err error) {
		mu.Lock()
		escalations = append(escalations, reason)
		mu.Unlock()
	}

	p := NewPool(&config.Config{})
	defer p.cancel()
	w := &Worker{ID: "dev-1", stop: make(chan struct{})}

	err := p.convergeQMIIdentity(context.Background(), w, "manual_reboot")
	if err == nil {
		t.Fatal("expected convergence to abort with error after persistent transport-down")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(escalations) != 1 || escalations[0] != "convergence_transport_down" {
		t.Fatalf("expected one convergence_transport_down escalation, got %v", escalations)
	}
}

func TestConvergeQMIIdentityEscalatesOnTimeout(t *testing.T) {
	origRefresh := convergeIdentityRefreshFn
	origEscalate := convergeEscalateFn
	origInterval := qmiConvergenceRetryInterval
	defer func() {
		convergeIdentityRefreshFn = origRefresh
		convergeEscalateFn = origEscalate
		qmiConvergenceRetryInterval = origInterval
	}()
	qmiConvergenceRetryInterval = time.Millisecond

	convergeIdentityRefreshFn = func(p *Pool, w *Worker, reason string) error {
		return errors.New("refresh_identity: live_identity_empty")
	}
	var mu sync.Mutex
	var escalations []string
	convergeEscalateFn = func(p *Pool, w *Worker, reason string, err error) {
		mu.Lock()
		escalations = append(escalations, reason)
		mu.Unlock()
	}

	p := NewPool(&config.Config{})
	defer p.cancel()
	w := &Worker{ID: "dev-1", stop: make(chan struct{})}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_ = p.convergeQMIIdentity(ctx, w, "manual_reboot")

	mu.Lock()
	defer mu.Unlock()
	if len(escalations) != 1 || escalations[0] != "convergence_timeout" {
		t.Fatalf("expected one convergence_timeout escalation, got %v", escalations)
	}
}
