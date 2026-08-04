package device

import (
	"strings"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestSuppressQMIUnhealthyEvictionDuringLifecycleRecovery(t *testing.T) {
	pool := NewPool(&config.Config{})
	worker := &Worker{
		ID: "dev1",
		Config: config.DeviceConfig{
			ID:            "dev1",
			DeviceBackend: backend.BackendQMI,
			ControlDevice: "/dev/cdc-wdm0",
		},
		Backend: &workerStatusBackendStub{mode: backend.BackendQMI, opModeErr: errBackendUnavailable{}},
	}
	pool.workers["dev1"] = worker
	pool.lifecycle.BeginRecovery("dev1", LifecyclePhaseQMIStarting, "modem_reboot", qmiLifecycleRecoveryTTL)

	suppressed, reason := pool.suppressQMIUnhealthyEviction(worker)
	if !suppressed {
		t.Fatal("expected lifecycle recovery to suppress eviction")
	}
	if !strings.Contains(reason, "lifecycle_qmi_starting") {
		t.Fatalf("reason=%q want contains lifecycle_qmi_starting", reason)
	}
}

func TestSuppressQMIUnhealthyEvictionAfterLifecycleDeadline(t *testing.T) {
	pool := NewPool(&config.Config{})
	worker := &Worker{
		ID: "dev1",
		Config: config.DeviceConfig{
			ID:            "dev1",
			DeviceBackend: backend.BackendQMI,
			ControlDevice: "/dev/cdc-wdm0",
		},
		Backend: &workerStatusBackendStub{mode: backend.BackendQMI, opModeErr: errBackendUnavailable{}},
	}
	now := time.Now().Add(-2 * qmiLifecycleRecoveryTTL)
	pool.lifecycle.BeginRecoveryAt("dev1", LifecyclePhaseRecovering, "modem_reboot", now, time.Second)

	suppressed, reason := pool.suppressQMIUnhealthyEviction(worker)
	if suppressed {
		t.Fatalf("suppressed=true want false reason=%q", reason)
	}
}

type errBackendUnavailable struct{}

func (errBackendUnavailable) Error() string { return "backend unavailable" }
