package api

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/device"
)

func TestOperatorSelectionErrorStatusMapsBusyStatesToConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "vowifi active", err: device.ErrVoWiFiActive, want: http.StatusConflict},
		{name: "esim switching", err: device.ErrESIMSwitching, want: http.StatusConflict},
		{name: "unsupported", err: device.ErrOperatorSelectionNotSupported, want: http.StatusBadRequest},
		{name: "backend missing", err: device.ErrBackendNotAvailable, want: http.StatusServiceUnavailable},
		{name: "generic", err: errors.New("qmi failed"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := operatorSelectionErrorStatus(tt.err); got != tt.want {
				t.Fatalf("operatorSelectionErrorStatus(%v)=%d want %d", tt.err, got, tt.want)
			}
		})
	}
}

func TestOperatorScanHTTPStatusAndBodyRunningWithoutCandidatesReturnsAccepted(t *testing.T) {
	now := time.Now()
	status, body := operatorScanHTTPStatusAndBody(device.OperatorScanResult{
		ScanID:    "scan-1",
		Status:    device.OperatorScanStatusRunning,
		StartedAt: now,
		UpdatedAt: now,
		Message:   "扫描进行中",
	})
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want %d", status, http.StatusAccepted)
	}
	if body.Status != string(device.OperatorScanStatusRunning) {
		t.Fatalf("body status=%q", body.Status)
	}
}

func TestOperatorScanHTTPStatusAndBodyRetryableFailureReturnsOK(t *testing.T) {
	now := time.Now()
	status, body := operatorScanHTTPStatusAndBody(device.OperatorScanResult{
		ScanID:    "scan-2",
		Status:    device.OperatorScanStatusFailed,
		StartedAt: now,
		UpdatedAt: now,
		Retryable: true,
		Message:   "扫描超时或模组忙，请稍后重试",
		Err:       "context deadline exceeded",
	})
	if status != http.StatusOK {
		t.Fatalf("status=%d want %d", status, http.StatusOK)
	}
	if !body.Retryable {
		t.Fatal("retryable=false want true")
	}
	if body.Message != "扫描超时或模组忙，请稍后重试" {
		t.Fatalf("message=%q", body.Message)
	}
}

func TestOperatorScanHTTPStatusAndBodyCompleteKeepsCandidates(t *testing.T) {
	now := time.Now()
	status, body := operatorScanHTTPStatusAndBody(device.OperatorScanResult{
		ScanID:     "scan-3",
		Status:     device.OperatorScanStatusComplete,
		StartedAt:  now,
		UpdatedAt:  now,
		Complete:   true,
		Message:    "扫描完成",
		Candidates: []backend.OperatorCandidate{{PLMN: "46000", OperatorName: "CMCC", Status: "available"}},
	})
	if status != http.StatusOK {
		t.Fatalf("status=%d want %d", status, http.StatusOK)
	}
	if len(body.Candidates) != 1 {
		t.Fatalf("len(candidates)=%d want 1", len(body.Candidates))
	}
}

func TestOperatorScanSSEShouldContinueOnlyForRunning(t *testing.T) {
	tests := []struct {
		name   string
		result device.OperatorScanResult
		want   bool
	}{
		{name: "running", result: device.OperatorScanResult{Status: device.OperatorScanStatusRunning}, want: true},
		{name: "complete", result: device.OperatorScanResult{Status: device.OperatorScanStatusComplete}, want: false},
		{name: "failed", result: device.OperatorScanResult{Status: device.OperatorScanStatusFailed}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := operatorScanSSEShouldContinue(tt.result); got != tt.want {
				t.Fatalf("operatorScanSSEShouldContinue(%s)=%v want %v", tt.result.Status, got, tt.want)
			}
		})
	}
}
