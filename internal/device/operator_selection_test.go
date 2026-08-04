package device

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestOperatorScanRetryableForMBIMVisibleProvidersTimeout(t *testing.T) {
	err := context.DeadlineExceeded
	if !isRetryableOperatorScanError(err) {
		t.Fatal("context deadline should remain retryable")
	}
	err = fmt.Errorf("mbim: VISIBLE_PROVIDERS status=2")
	if !isRetryableOperatorScanError(err) {
		t.Fatal("MBIM visible providers busy/status error should be retryable")
	}
}

type mockOperatorProvider struct {
	backend.DeviceBackend
	scanCalled atomic.Bool
	scanCalls  atomic.Int32
	getCalled  atomic.Bool
	setCalled  atomic.Bool
	scanErr    error
	scanBlock  chan struct{}
	partial    []backend.OperatorCandidate
	complete   bool
	partialTS  time.Time
	partialOK  bool
}

func (m *mockOperatorProvider) Mode() string { return "mock" }
func (m *mockOperatorProvider) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	return backend.ModeOnline, nil
}
func (m *mockOperatorProvider) GetServingSystem(ctx context.Context) (*backend.ServingSystem, error) {
	return &backend.ServingSystem{}, nil
}
func (m *mockOperatorProvider) GetIMEI(ctx context.Context) (string, error)     { return "123", nil }
func (m *mockOperatorProvider) GetIMSI(ctx context.Context) (string, error)     { return "456", nil }
func (m *mockOperatorProvider) GetICCID(ctx context.Context) (string, error)    { return "789", nil }
func (m *mockOperatorProvider) GetMSISDN(ctx context.Context) (string, error)   { return "000", nil }
func (m *mockOperatorProvider) GetRevision(ctx context.Context) (string, error) { return "rev1", nil }
func (m *mockOperatorProvider) GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error) {
	return &backend.SignalInfo{}, nil
}
func (m *mockOperatorProvider) IsSimInserted(ctx context.Context) (bool, error) { return true, nil }
func (m *mockOperatorProvider) GetNativeMCCMNC(ctx context.Context) (string, string, error) {
	return "460", "00", nil
}
func (m *mockOperatorProvider) GetNativeSPN(ctx context.Context) (string, error) { return "CMCC", nil }
func (m *mockOperatorProvider) GetSIMMetadata(ctx context.Context) (*backend.SIMMetadata, error) {
	return &backend.SIMMetadata{}, nil
}

func (m *mockOperatorProvider) ScanOperators(ctx context.Context) ([]backend.OperatorCandidate, error) {
	m.scanCalled.Store(true)
	m.scanCalls.Add(1)
	if m.scanBlock != nil {
		select {
		case <-m.scanBlock:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, m.scanErr
}

func (m *mockOperatorProvider) GetOperatorSelection(ctx context.Context) (backend.OperatorSelection, error) {
	m.getCalled.Store(true)
	return backend.OperatorSelection{Mode: backend.OperatorSelectionAutomatic}, nil
}

func (m *mockOperatorProvider) SetOperatorSelection(ctx context.Context, req backend.SetOperatorSelectionRequest) (backend.OperatorSelection, error) {
	m.setCalled.Store(true)
	return backend.OperatorSelection{}, nil
}

func (m *mockOperatorProvider) IncrementalOperatorScanSnapshot() ([]backend.OperatorCandidate, bool, time.Time, bool) {
	return m.partial, m.complete, m.partialTS, m.partialOK
}

func TestWorker_OperatorSelection_NilWorker(t *testing.T) {
	var w *Worker
	_, err := w.ScanOperators(context.Background())
	if err != ErrWorkerNil {
		t.Fatalf("expected ErrWorkerNil, got %v", err)
	}
}

func TestWorker_OperatorSelection_BackendNotAvailable(t *testing.T) {
	w := &Worker{}
	_, err := w.ScanOperators(context.Background())
	if err != ErrBackendNotAvailable {
		t.Fatalf("expected ErrBackendNotAvailable, got %v", err)
	}
}

func TestWorker_SetOperatorSelection_Success(t *testing.T) {
	provider := &mockOperatorProvider{}
	w := &Worker{Backend: provider}

	req := backend.SetOperatorSelectionRequest{
		Mode: backend.OperatorSelectionManual,
		PLMN: "46000",
		RAT:  backend.OperatorRATLTE,
	}

	_, err := w.SetOperatorSelection(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !provider.setCalled.Load() {
		t.Fatalf("expected setCalled=true")
	}

	if w.Config.OperatorSelectionMode != string(req.Mode) || w.Config.OperatorSelectionPLMN != req.PLMN || w.Config.OperatorSelectionRAT != string(req.RAT) {
		t.Fatalf("config not updated properly: %+v", w.Config)
	}
}

func TestWorker_GetOperatorSelectionPrefersManualConfig(t *testing.T) {
	provider := &mockOperatorProvider{}
	w := &Worker{
		Backend: provider,
		Config: config.DeviceConfig{
			OperatorSelectionMode: "manual",
			OperatorSelectionPLMN: "310260",
			OperatorSelectionRAT:  string(backend.OperatorRATLTE),
		},
	}

	got, err := w.GetOperatorSelection(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if provider.getCalled.Load() {
		t.Fatal("backend GetOperatorSelection should not be called when manual config is valid")
	}
	if got.Mode != backend.OperatorSelectionManual || got.PLMN != "310260" || got.MCC != "310" || got.MNC != "260" || !got.IncludesPCSDigit || got.RAT != backend.OperatorRATLTE {
		t.Fatalf("unexpected selection from config: %+v", got)
	}
}

func TestWorker_StartOrGetOperatorScanReturnsExistingInFlightTask(t *testing.T) {
	provider := &mockOperatorProvider{scanBlock: make(chan struct{})}
	w := &Worker{ID: "dev-1", Backend: provider}

	first := w.StartOrGetOperatorScan(context.Background())
	waitUntilOperatorScanCalls(t, provider, 1, time.Second)
	second := w.StartOrGetOperatorScan(context.Background())
	close(provider.scanBlock)

	if first.ScanID == "" || second.ScanID == "" {
		t.Fatalf("scan IDs should be populated: first=%q second=%q", first.ScanID, second.ScanID)
	}
	if first.ScanID != second.ScanID {
		t.Fatalf("scan id mismatch: first=%q second=%q", first.ScanID, second.ScanID)
	}
	if provider.scanCalls.Load() != 1 {
		t.Fatalf("scanCalls=%d want 1", provider.scanCalls.Load())
	}
}

func TestWorker_StartOrGetOperatorScanClassifiesDeadlineAsRetryable(t *testing.T) {
	provider := &mockOperatorProvider{scanErr: context.DeadlineExceeded}
	w := &Worker{ID: "dev-1", Backend: provider}

	first := w.StartOrGetOperatorScan(context.Background())
	if first.Status != OperatorScanStatusRunning {
		t.Fatalf("initial status=%s want %s", first.Status, OperatorScanStatusRunning)
	}

	waitUntilOperatorScanSettled(t, w, time.Second)
	got := w.GetOperatorScanSnapshot()
	if got.Status != OperatorScanStatusFailed {
		t.Fatalf("status=%s want %s", got.Status, OperatorScanStatusFailed)
	}
	if !got.Retryable {
		t.Fatal("retryable=false want true")
	}
	if got.Message != "扫描超时或模组忙，请稍后重试" {
		t.Fatalf("message=%q", got.Message)
	}
}

func TestWorker_StartOrGetOperatorScanClassifiesQMIInternalAsRetryable(t *testing.T) {
	provider := &mockOperatorProvider{
		scanErr: &qmi.QMIError{
			Service:   qmi.ServiceNAS,
			MessageID: qmi.NASPerformNetworkScan,
			Result:    1,
			ErrorCode: qmi.QMIErrInternal,
		},
	}
	w := &Worker{ID: "dev-1", Backend: provider}

	w.StartOrGetOperatorScan(context.Background())
	waitUntilOperatorScanSettled(t, w, time.Second)
	got := w.GetOperatorScanSnapshot()
	if got.Status != OperatorScanStatusFailed {
		t.Fatalf("status=%s want %s", got.Status, OperatorScanStatusFailed)
	}
	if !got.Retryable {
		t.Fatal("retryable=false want true")
	}
}

func TestWorker_StartOrGetOperatorScanExpiresStaleRunningTask(t *testing.T) {
	provider := &mockOperatorProvider{scanBlock: make(chan struct{})}
	w := &Worker{ID: "dev-1", Backend: provider}
	expired := time.Now().Add(-operatorScanTimeout - time.Second)
	w.operatorScanCurrent = OperatorScanResult{
		ScanID:    "stale-scan",
		Status:    OperatorScanStatusRunning,
		StartedAt: expired,
		UpdatedAt: expired,
		Message:   "扫描进行中",
	}
	w.operatorScanActive = true
	w.operatorScanCancel = func() {}

	got := w.StartOrGetOperatorScan(context.Background())
	if got.ScanID != "stale-scan" {
		t.Fatalf("scan_id=%q want stale-scan; polling an expired scan should not start a new task", got.ScanID)
	}
	if got.Status != OperatorScanStatusFailed {
		t.Fatalf("status=%s want %s", got.Status, OperatorScanStatusFailed)
	}
	if !got.Retryable {
		t.Fatal("retryable=false want true")
	}
	if got.Message != "扫描超时或模组忙，请稍后重试" {
		t.Fatalf("message=%q", got.Message)
	}
	if w.IsOperatorScanActive() {
		t.Fatal("operator scan should not remain active after stale timeout")
	}
	if provider.scanCalls.Load() != 0 {
		t.Fatalf("scanCalls=%d want 0", provider.scanCalls.Load())
	}
}

func TestWorker_StartOrGetOperatorScanMergesIncrementalSnapshot(t *testing.T) {
	ts := time.Now().Add(50 * time.Millisecond)
	provider := &mockOperatorProvider{
		scanBlock: make(chan struct{}),
		partial:   []backend.OperatorCandidate{{PLMN: "46000", OperatorName: "CMCC", Status: "available"}},
		complete:  false,
		partialTS: ts,
		partialOK: true,
	}
	w := &Worker{ID: "dev-1", Backend: provider}

	first := w.StartOrGetOperatorScan(context.Background())
	if first.Status != OperatorScanStatusRunning {
		t.Fatalf("initial status=%s want %s", first.Status, OperatorScanStatusRunning)
	}

	got := w.StartOrGetOperatorScan(context.Background())
	close(provider.scanBlock)
	if len(got.Candidates) != 1 {
		t.Fatalf("len(candidates)=%d want 1", len(got.Candidates))
	}
	if got.Candidates[0].PLMN != "46000" {
		t.Fatalf("unexpected candidates=%+v", got.Candidates)
	}
}

func TestIsRetryableOperatorScanError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "deadline", err: context.DeadlineExceeded, want: true},
		{name: "canceled", err: context.Canceled, want: true},
		{name: "nas internal", err: &qmi.QMIError{Service: qmi.ServiceNAS, MessageID: qmi.NASPerformNetworkScan, ErrorCode: qmi.QMIErrInternal}, want: true},
		{name: "wrapped nas internal", err: errors.New((&qmi.QMIError{Service: qmi.ServiceNAS, MessageID: qmi.NASPerformNetworkScan, ErrorCode: qmi.QMIErrInternal}).Error()), want: false},
		{name: "other", err: errors.New("other"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetryableOperatorScanError(tt.err); got != tt.want {
				t.Fatalf("isRetryableOperatorScanError(%v)=%v want %v", tt.err, got, tt.want)
			}
		})
	}
}

func waitUntilOperatorScanSettled(t *testing.T, w *Worker, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		got := w.GetOperatorScanSnapshot()
		if got.Status == OperatorScanStatusFailed || got.Status == OperatorScanStatusComplete {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("operator scan did not settle")
}

func waitUntilOperatorScanCalls(t *testing.T, provider *mockOperatorProvider, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if provider.scanCalls.Load() == int32(want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("scanCalls=%d want %d", provider.scanCalls.Load(), want)
}
