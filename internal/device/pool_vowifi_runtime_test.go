package device

import (
	"context"
	"errors"
	"testing"
	"time"

	qmimanager "github.com/iniwex5/quectel-qmi-go/pkg/manager"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

type mockReadinessBackend struct {
	backend.DeviceBackend
	r qmimanager.UIMReadiness
	e error
}

func (m *mockReadinessBackend) GetUIMReadiness(ctx context.Context) (qmimanager.UIMReadiness, error) {
	return m.r, m.e
}

func TestWaitUIMIdentityReady_ReadyWithIdentity(t *testing.T) {
	p := NewPool(&config.Config{})
	w := &Worker{ID: "test-mbim", Backend: &mockReadinessBackend{
		r: qmimanager.UIMReadiness{
			Reason: qmimanager.UIMReadinessReady,
			ICCID:  "123",
			IMSI:   "456",
		},
	}}
	p.mu.Lock()
	p.workers["test-mbim"] = w
	p.mu.Unlock()

	err := p.WaitQMICoreReady("test-mbim", 1*time.Second)
	if err != nil {
		t.Fatalf("expected nil error when identity is ready, got %v", err)
	}
}

func TestWaitQMICoreReady_IdentityEmpty(t *testing.T) {
	p := NewPool(&config.Config{})
	w := &Worker{ID: "test-mbim", Backend: &mockReadinessBackend{
		r: qmimanager.UIMReadiness{
			Reason: qmimanager.UIMReadinessIdentityEmpty,
		},
	}}
	p.mu.Lock()
	p.workers["test-mbim"] = w
	p.mu.Unlock()

	err := p.WaitQMICoreReady("test-mbim", 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout (DeadlineExceeded) when identity is empty, got %v", err)
	}
}

func TestWaitQMICoreReady_CardAbsent(t *testing.T) {
	p := NewPool(&config.Config{})
	w := &Worker{ID: "test-mbim", Backend: &mockReadinessBackend{
		r: qmimanager.UIMReadiness{
			Reason:      qmimanager.UIMReadinessCardAbsent,
			CardPresent: false,
		},
	}}
	p.mu.Lock()
	p.workers["test-mbim"] = w
	p.mu.Unlock()

	err := p.WaitQMICoreReady("test-mbim", 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout (DeadlineExceeded) when card is absent, got %v", err)
	}
}

func TestWaitQMICoreReady_TransportFatal(t *testing.T) {
	p := NewPool(&config.Config{})
	w := &Worker{ID: "test-mbim", Backend: &mockReadinessBackend{
		r: qmimanager.UIMReadiness{
			Reason:         qmimanager.UIMReadinessTransportFatal,
			TransportReady: false,
		},
	}}
	p.mu.Lock()
	p.workers["test-mbim"] = w
	p.mu.Unlock()

	err := p.WaitQMICoreReady("test-mbim", 100*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout (DeadlineExceeded) when transport is fatal, got %v", err)
	}
}
