package device

import (
	"context"
	"errors"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

type smscResult struct {
	value string
	err   error
}

type workerSMSCBackendStub struct {
	workerStatusBackendStub
	seq   []smscResult
	calls int
}

func (s *workerSMSCBackendStub) GetSMSC(ctx context.Context) (string, error) {
	s.calls++
	if len(s.seq) == 0 {
		return "", nil
	}
	out := s.seq[0]
	s.seq = s.seq[1:]
	return out.value, out.err
}

func TestWorkerGetSMSCWithContextQMIRequiresSMSCProvider(t *testing.T) {
	w := &Worker{
		ID:      "dev-qmi",
		Backend: &workerStatusBackendStub{mode: backend.BackendQMI},
	}
	got, err := w.getSMSCWithContext(context.Background())
	if err == nil {
		t.Fatal("expected error for qmi backend without SMSCProvider")
	}
	if got != "" {
		t.Fatalf("getSMSCWithContext()=%q want empty", got)
	}
}

func TestWorkerGetSMSCWithContextATUsesProvider(t *testing.T) {
	b := &workerSMSCBackendStub{
		workerStatusBackendStub: workerStatusBackendStub{mode: backend.BackendAT},
		seq: []smscResult{
			{value: "+8613800250500"},
		},
	}
	w := &Worker{
		ID:      "dev-at",
		Backend: b,
	}
	got, err := w.getSMSCWithContext(context.Background())
	if err != nil {
		t.Fatalf("getSMSCWithContext() error=%v", err)
	}
	if got != "+8613800250500" {
		t.Fatalf("getSMSCWithContext()=%q want=%q", got, "+8613800250500")
	}
}

func TestWorkerGetSMSCWithContextFallsBackToConfiguredSMSC(t *testing.T) {
	b := &workerSMSCBackendStub{
		workerStatusBackendStub: workerStatusBackendStub{mode: backend.BackendAT},
		seq: []smscResult{
			{err: errors.New("AT+CSCA failed")},
		},
	}
	w := &Worker{
		ID:      "dev-at",
		Config:  config.DeviceConfig{SMSC: " +8613800250500 "},
		Backend: b,
	}

	got, err := w.getSMSCWithContext(context.Background())
	if err != nil {
		t.Fatalf("getSMSCWithContext() error=%v", err)
	}
	if got != "+8613800250500" {
		t.Fatalf("getSMSCWithContext()=%q want configured fallback", got)
	}
}
