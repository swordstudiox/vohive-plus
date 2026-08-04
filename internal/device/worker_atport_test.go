package device

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func TestWorkerResolvedATPortPrefersConfig(t *testing.T) {
	w := &Worker{Config: config.DeviceConfig{ATPort: "/dev/ttyUSB7"}}
	if got := w.ResolvedATPort(); got != "/dev/ttyUSB7" {
		t.Fatalf("ResolvedATPort() = %q, want /dev/ttyUSB7", got)
	}
}

func TestWorkerResolvedATPortFallsBackToManagePort(t *testing.T) {
	w := &Worker{Config: config.DeviceConfig{ManagePort: "/dev/ttyUSB3"}}
	if got := w.ResolvedATPort(); got != "/dev/ttyUSB3" {
		t.Fatalf("ResolvedATPort() = %q, want /dev/ttyUSB3", got)
	}
}

// 关键场景:零路径架构下 worker.Config 的 AT 口可能为空,
// 但 Modem 在设备获取时刻已把端口快照在内存里——必须兜底到它。
func TestWorkerResolvedATPortFallsBackToModem(t *testing.T) {
	m, err := modem.New(config.DeviceConfig{ID: "d", DeviceBackend: "qmi", ManagePort: "/dev/ttyUSB2"})
	if err != nil {
		t.Fatalf("modem.New() error = %v", err)
	}
	w := &Worker{Config: config.DeviceConfig{ID: "d", DeviceBackend: "qmi"}, Modem: m}
	if got := w.ResolvedATPort(); got != "/dev/ttyUSB2" {
		t.Fatalf("ResolvedATPort() = %q, want /dev/ttyUSB2", got)
	}
}

func TestWorkerResolvedATPortNilSafe(t *testing.T) {
	var w *Worker
	if got := w.ResolvedATPort(); got != "" {
		t.Fatalf("ResolvedATPort() = %q, want empty", got)
	}
}
