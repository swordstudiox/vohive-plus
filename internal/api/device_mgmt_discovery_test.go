package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/device"
)

// 不同 IMEI 落在已配置设备的旧路径上,不再是"冲突":新模组就是一台可正常添加的设备,
// 旧配置离线(身份锚定后路径不再有否决权)。
func TestHandleDeviceMgmtDiscoveredDifferentIMEIIsPlainAddable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := writeDeviceMgmtDiscoveryConfig(t, `
server:
  port: ":7575"
devices:
  - id: old-device
    modem_imei: "111111111111111"
    control_device: /dev/cdc-wdm0
    interface: wwan0
    usb_path: /sys/bus/usb/devices/1-1
    at_port: /dev/ttyUSB2
    device_backend: qmi
`)
	if err := config.InitGlobalManager(path); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	restoreDiscoveryStubs(t)
	discoverQMIForMgmtFn = func() ([]device.QMIDevice, error) { return nil, nil }
	discoverCompatibleModemsFromQMIFn = func([]device.QMIDevice) ([]device.CompatibleModem, error) {
		return []device.CompatibleModem{{
			ControlPath:    "/dev/cdc-wdm0",
			NetInterface:   "wwan0",
			USBPath:        "/sys/bus/usb/devices/1-1",
			ATPorts:        []string{"/dev/ttyUSB2"},
			ATPort:         "/dev/ttyUSB2",
			Mode:           "qmi",
			NetworkCapable: true,
		}}, nil
	}
	enrichDiscoveredCompatibleModemFn = func(dev device.CompatibleModem, opts device.CompatibleModemEnrichOptions) (device.CompatibleModem, string) {
		dev.IMEI = "222222222222222"
		return dev, "222222222222222"
	}

	got := requestDiscoveredDevices(t, &Server{pool: device.NewPool(&config.Config{}), configPath: path})
	if len(got.Devices) != 1 {
		t.Fatalf("devices len = %d, want 1", len(got.Devices))
	}
	d := got.Devices[0]
	if d.Configured {
		t.Fatalf("Configured = true, want false: %+v", d)
	}
	if d.Degraded {
		t.Fatalf("Degraded = true, want false (IMEI is readable): %+v", d)
	}
	if d.IMEI != "222222222222222" {
		t.Fatalf("IMEI = %q, want new device IMEI", d.IMEI)
	}
}

// 探不到 IMEI 的硬件(如 MBIM 挂死返回垃圾)归 degraded:不可直接添加,UI 需提示。
func TestHandleDeviceMgmtDiscoveredMarksDegradedWhenIMEIUnreadable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := writeDeviceMgmtDiscoveryConfig(t, `
server:
  port: ":7575"
devices: []
`)
	if err := config.InitGlobalManager(path); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	restoreDiscoveryStubs(t)
	discoverQMIForMgmtFn = func() ([]device.QMIDevice, error) { return nil, nil }
	discoverCompatibleModemsFromQMIFn = func([]device.QMIDevice) ([]device.CompatibleModem, error) {
		return []device.CompatibleModem{{
			ControlPath:  "/dev/cdc-wdm1",
			NetInterface: "wwan3",
			USBPath:      "/sys/bus/usb/devices/1-9",
			Mode:         "mbim",
		}}, nil
	}
	enrichDiscoveredCompatibleModemFn = func(dev device.CompatibleModem, opts device.CompatibleModemEnrichOptions) (device.CompatibleModem, string) {
		return dev, "" // AT/QMI 探不到 IMEI
	}
	probeIMEIViaMBIMForMgmtFn = func(string) (string, error) { return "", fmt.Errorf("mbim hung") } // MBIM 也读不到

	got := requestDiscoveredDevices(t, &Server{pool: device.NewPool(&config.Config{}), configPath: path})
	if len(got.Devices) != 1 {
		t.Fatalf("devices len = %d, want 1", len(got.Devices))
	}
	d := got.Devices[0]
	if d.Configured || !d.Degraded {
		t.Fatalf("want Configured=false Degraded=true, got %+v", d)
	}
}

func TestHandleDeviceMgmtDiscoveredMarksConfiguredForSameIMEI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := writeDeviceMgmtDiscoveryConfig(t, `
server:
  port: ":7575"
devices:
  - id: old-device
    modem_imei: "111111111111111"
    control_device: /dev/cdc-wdm0
    interface: wwan0
    usb_path: /sys/bus/usb/devices/1-1
    at_port: /dev/ttyUSB2
    device_backend: qmi
`)
	if err := config.InitGlobalManager(path); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	restoreDiscoveryStubs(t)
	discoverQMIForMgmtFn = func() ([]device.QMIDevice, error) { return nil, nil }
	discoverCompatibleModemsFromQMIFn = func([]device.QMIDevice) ([]device.CompatibleModem, error) {
		return []device.CompatibleModem{{
			ControlPath:  "/dev/cdc-wdm0",
			NetInterface: "wwan0",
			USBPath:      "/sys/bus/usb/devices/1-1",
			ATPorts:      []string{"/dev/ttyUSB2"},
			ATPort:       "/dev/ttyUSB2",
			Mode:         "qmi",
		}}, nil
	}
	enrichDiscoveredCompatibleModemFn = func(dev device.CompatibleModem, opts device.CompatibleModemEnrichOptions) (device.CompatibleModem, string) {
		dev.IMEI = "111111111111111"
		return dev, "111111111111111"
	}

	got := requestDiscoveredDevices(t, &Server{pool: device.NewPool(&config.Config{}), configPath: path})
	d := got.Devices[0]
	if !d.Configured || d.ConfiguredID != "old-device" {
		t.Fatalf("configured fields = %+v, want configured old-device", d)
	}
	if d.Degraded {
		t.Fatalf("Degraded = true, want false: %+v", d)
	}
}

// TestHandleDeviceMgmtDiscoveredLegacyPathConfigDegrades verifies that after the
// zero-path migration, a device configured with only path fields (no IMEI) can no
// longer be matched by legacyPathMatch because the migration scrubs those keys from
// disk on Load(). The discovered device appears as Degraded — this is the accepted
// behavioral risk of Option A (unconditional deprecation).
func TestHandleDeviceMgmtDiscoveredLegacyPathConfigDegrades(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := writeDeviceMgmtDiscoveryConfig(t, `
server:
  port: ":7575"
devices:
  - id: legacy-device
    control_device: /dev/cdc-wdm0
    interface: wwan0
    usb_path: /sys/bus/usb/devices/1-1
    at_port: /dev/ttyUSB2
    device_backend: qmi
`)
	if err := config.InitGlobalManager(path); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	restoreDiscoveryStubs(t)
	discoverQMIForMgmtFn = func() ([]device.QMIDevice, error) { return nil, nil }
	discoverCompatibleModemsFromQMIFn = func([]device.QMIDevice) ([]device.CompatibleModem, error) {
		return []device.CompatibleModem{{
			ControlPath:  "/dev/cdc-wdm0",
			NetInterface: "wwan0",
			USBPath:      "/sys/bus/usb/devices/1-1",
			ATPorts:      []string{"/dev/ttyUSB2"},
			ATPort:       "/dev/ttyUSB2",
			Mode:         "qmi",
		}}, nil
	}
	enrichDiscoveredCompatibleModemFn = func(dev device.CompatibleModem, opts device.CompatibleModemEnrichOptions) (device.CompatibleModem, string) {
		return dev, ""
	}

	got := requestDiscoveredDevices(t, &Server{pool: device.NewPool(&config.Config{}), configPath: path})
	d := got.Devices[0]
	// 零路径迁移后:磁盘路径键已删,legacyPathMatch 无法命中,设备归入 degraded。
	if d.Configured {
		t.Fatalf("device should not be matched after path migration, got: %+v", d)
	}
	if !d.Degraded {
		t.Fatalf("device should be degraded (no IMEI, no matched config), got: %+v", d)
	}
}

type discoveredDevicesResponse struct {
	Devices []discoveredDevice `json:"devices"`
}

func requestDiscoveredDevices(t *testing.T, srv *Server) discoveredDevicesResponse {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/devices/discovered?with_imei=1", nil)

	srv.handleDeviceMgmtDiscovered(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", recorder.Code, recorder.Body.String())
	}
	var resp discoveredDevicesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON error = %v body=%s", err, recorder.Body.String())
	}
	return resp
}

func restoreDiscoveryStubs(t *testing.T) {
	t.Helper()
	origDiscoverQMI := discoverQMIForMgmtFn
	origDiscoverCompat := discoverCompatibleModemsFromQMIFn
	origEnrich := enrichDiscoveredCompatibleModemFn

	origProbeMBIM := probeIMEIViaMBIMForMgmtFn
	t.Cleanup(func() {
		discoverQMIForMgmtFn = origDiscoverQMI
		discoverCompatibleModemsFromQMIFn = origDiscoverCompat
		enrichDiscoveredCompatibleModemFn = origEnrich

		probeIMEIViaMBIMForMgmtFn = origProbeMBIM
	})
}

func writeDeviceMgmtDiscoveryConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
