package device

import (
	"fmt"
	"testing"
	"time"

	qmiq "github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestEnrichDiscoveredQMIDeviceHonorsQMIIMEIProbeFlag(t *testing.T) {
	origATProbe := probeIMEICachedFn
	origQMIProbe := probeIMEIViaQMIFn
	t.Cleanup(func() {
		probeIMEICachedFn = origATProbe
		probeIMEIViaQMIFn = origQMIProbe
	})

	probeIMEICachedFn = func(atPort string, timeout time.Duration) (string, error) {
		return "", fmt.Errorf("no at imei")
	}

	qmiCalls := 0
	probeIMEIViaQMIFn = func(controlPath string, opts qmiq.ClientOptions) (string, error) {
		qmiCalls++
		return "867123456789012", nil
	}

	dev := QMIDevice{
		ControlPath: "/dev/cdc-wdm9",
		ATPort:      "/dev/ttyUSB6",
		ATPorts:     []string{"/dev/ttyUSB6"},
	}

	got, imei := EnrichDiscoveredQMIDevice(dev, QMIDeviceEnrichOptions{
		EnableATProbe:      true,
		ATProbeTimeout:     50 * time.Millisecond,
		EnableQMIIMEIProbe: false,
	})
	if qmiCalls != 0 {
		t.Fatalf("qmiCalls=%d want=0", qmiCalls)
	}
	if imei != "" {
		t.Fatalf("imei=%q want empty", imei)
	}

	got, imei = EnrichDiscoveredQMIDevice(dev, QMIDeviceEnrichOptions{
		EnableATProbe:      true,
		ATProbeTimeout:     50 * time.Millisecond,
		EnableQMIIMEIProbe: true,
	})
	if qmiCalls != 1 {
		t.Fatalf("qmiCalls=%d want=1", qmiCalls)
	}
	if imei != "867123456789012" {
		t.Fatalf("imei=%q want 867123456789012", imei)
	}
	if got.ControlPath != dev.ControlPath {
		t.Fatalf("ControlPath=%q want %q", got.ControlPath, dev.ControlPath)
	}
}

func TestResolveDiscoveredQMIDeviceDoesNotProbeATPort(t *testing.T) {
	origATProbe := probeIMEICachedFn
	origQMIProbe := probeIMEIViaQMIFn
	t.Cleanup(func() {
		probeIMEICachedFn = origATProbe
		probeIMEIViaQMIFn = origQMIProbe
	})

	atCalls := 0
	probeIMEICachedFn = func(atPort string, timeout time.Duration) (string, error) {
		atCalls++
		return "should-not-use-at", nil
	}
	probeIMEIViaQMIFn = func(controlPath string, opts qmiq.ClientOptions) (string, error) {
		return "867123456789012", nil
	}

	dev := QMIDevice{
		ControlPath: "/dev/cdc-wdm9",
		ATPort:      "/dev/ttyUSB6",
		ATPorts:     []string{"/dev/ttyUSB6"},
	}

	_, imei := resolveDiscoveredQMIDevice(dev, 50*time.Millisecond, true)
	if atCalls != 0 {
		t.Fatalf("AT probe calls = %d, want 0 for pure QMI discovery", atCalls)
	}
	if imei != "867123456789012" {
		t.Fatalf("imei=%q want QMI-derived IMEI", imei)
	}
}

func TestResolveDiscoveredQMIDevicePureQMIUsesQMIIMEIProbe(t *testing.T) {
	origATProbe := probeIMEICachedFn
	origQMIProbe := probeIMEIViaQMIFn
	t.Cleanup(func() {
		probeIMEICachedFn = origATProbe
		probeIMEIViaQMIFn = origQMIProbe
	})

	atCalls := 0
	qmiCalls := 0
	probeIMEICachedFn = func(atPort string, timeout time.Duration) (string, error) {
		atCalls++
		return "", fmt.Errorf("unexpected AT probe on %s", atPort)
	}
	probeIMEIViaQMIFn = func(controlPath string, opts qmiq.ClientOptions) (string, error) {
		qmiCalls++
		if controlPath != "/dev/cdc-wdm5" {
			t.Fatalf("controlPath=%q want /dev/cdc-wdm5", controlPath)
		}
		return "359762080000001", nil
	}

	dev, imei := resolveDiscoveredQMIDevice(QMIDevice{
		ControlPath:  "/dev/cdc-wdm5",
		DriverName:   "qmi_wwan",
		NetInterface: "wwan5",
		ATPorts:      nil,
		ATPort:       "",
	}, 50*time.Millisecond, true)

	if imei != "359762080000001" {
		t.Fatalf("imei=%q want 359762080000001", imei)
	}
	if dev.ATPort != "" {
		t.Fatalf("ATPort=%q want empty", dev.ATPort)
	}
	if atCalls != 0 {
		t.Fatalf("AT probe calls=%d want 0", atCalls)
	}
	if qmiCalls != 1 {
		t.Fatalf("QMI probe calls=%d want 1", qmiCalls)
	}
}

func TestResolveDiscoveredQMIDevicePassesDefaultQMIClientOptions(t *testing.T) {
	origQMIProbe := probeIMEIViaQMIFn
	t.Cleanup(func() {
		probeIMEIViaQMIFn = origQMIProbe
	})

	var gotOpts qmiq.ClientOptions
	probeIMEIViaQMIFn = func(controlPath string, opts qmiq.ClientOptions) (string, error) {
		gotOpts = opts
		return "867123456789012", nil
	}

	_, imei := resolveDiscoveredQMIDevice(QMIDevice{ControlPath: "/dev/cdc-wdm9"}, 50*time.Millisecond, true)
	if imei != "867123456789012" {
		t.Fatalf("imei=%q want 867123456789012", imei)
	}
	if !gotOpts.SyncOnOpen {
		t.Fatal("SyncOnOpen=false, want default QMI client options")
	}
	if gotOpts.DefaultRequestTimeout != 30*time.Second {
		t.Fatalf("DefaultRequestTimeout=%s, want 30s", gotOpts.DefaultRequestTimeout)
	}
}

func TestEnrichDiscoveredQMIDevicePassesQMIClientOptions(t *testing.T) {
	origQMIProbe := probeIMEIViaQMIFn
	t.Cleanup(func() {
		probeIMEIViaQMIFn = origQMIProbe
	})

	var gotPath string
	var gotOpts qmiq.ClientOptions
	probeIMEIViaQMIFn = func(controlPath string, opts qmiq.ClientOptions) (string, error) {
		gotPath = controlPath
		gotOpts = opts
		return "867123456789012", nil
	}

	dev := QMIDevice{ControlPath: "/dev/cdc-wdm9"}
	_, imei := EnrichDiscoveredQMIDevice(dev, QMIDeviceEnrichOptions{
		EnableQMIIMEIProbe: true,
		QMIClientOptions: qmiq.ClientOptions{
			UseProxy:        true,
			ProxyPath:       "custom-qmi-proxy",
			ProxyExecutable: "/opt/vohive/bin/qmi-proxy",
		},
	})

	if imei != "867123456789012" {
		t.Fatalf("imei=%q want 867123456789012", imei)
	}
	if gotPath != "/dev/cdc-wdm9" {
		t.Fatalf("controlPath=%q want /dev/cdc-wdm9", gotPath)
	}
	if !gotOpts.UseProxy {
		t.Fatal("UseProxy=false, want true")
	}
	if gotOpts.ProxyPath != "custom-qmi-proxy" {
		t.Fatalf("ProxyPath=%q, want custom-qmi-proxy", gotOpts.ProxyPath)
	}
	if gotOpts.ProxyExecutable != "/opt/vohive/bin/qmi-proxy" {
		t.Fatalf("ProxyExecutable=%q, want /opt/vohive/bin/qmi-proxy", gotOpts.ProxyExecutable)
	}
}

func TestStaticQMIDeviceIndexLookupPriority(t *testing.T) {
	idx := BuildStaticQMIDeviceIndex([]QMIDevice{
		{
			ControlPath:  "/dev/cdc-wdm0",
			USBPath:      "/sys/bus/usb/devices/1-1",
			NetInterface: "wwan0",
		},
		{
			ControlPath:  "/dev/cdc-wdm1",
			USBPath:      "/sys/bus/usb/devices/1-2",
			NetInterface: "wwan1",
		},
	})

	if got, ok := idx.Lookup("/dev/cdc-wdm1", "/sys/bus/usb/devices/1-1", "wwan0"); !ok || got.ControlPath != "/dev/cdc-wdm1" {
		t.Fatalf("lookup by control failed: ok=%v got=%+v", ok, got)
	}
	if got, ok := idx.Lookup("", "/sys/bus/usb/devices/1-1", "wwan1"); !ok || got.USBPath != "/sys/bus/usb/devices/1-1" {
		t.Fatalf("lookup by usb failed: ok=%v got=%+v", ok, got)
	}
	if got, ok := idx.Lookup("", "", "wwan1"); !ok || got.NetInterface != "wwan1" {
		t.Fatalf("lookup by iface failed: ok=%v got=%+v", ok, got)
	}
}

func TestConfiguredDeviceIndexLookupFallsBackToIMEI(t *testing.T) {
	idx := BuildConfiguredDeviceIndex([]config.DeviceConfig{
		{
			ID:            "dev-a",
			ControlDevice: "/dev/cdc-wdm0",
			USBPath:       "/sys/bus/usb/devices/1-1",
			Interface:     "wwan0",
			ModemIMEI:     "867123456789012",
		},
		{
			ID:        "dev-b",
			ModemIMEI: "867123456789099",
		},
	})

	if got := idx.Lookup("/dev/cdc-wdm0", "", "", ""); got != "dev-a" {
		t.Fatalf("lookup by control=%q want dev-a", got)
	}
	if got := idx.Lookup("", "/sys/bus/usb/devices/1-1", "", ""); got != "dev-a" {
		t.Fatalf("lookup by usb=%q want dev-a", got)
	}
	if got := idx.Lookup("", "", "wwan0", ""); got != "dev-a" {
		t.Fatalf("lookup by iface=%q want dev-a", got)
	}
	if got := idx.Lookup("", "", "", "867123456789099"); got != "dev-b" {
		t.Fatalf("lookup by imei=%q want dev-b", got)
	}
}

func TestConfiguredDeviceIndexSeparatesIdentityAndDynamicPath(t *testing.T) {
	idx := BuildConfiguredDeviceIndex([]config.DeviceConfig{
		{
			ID:            "old-device",
			ControlDevice: "/dev/cdc-wdm0",
			USBPath:       "/sys/bus/usb/devices/1-1",
			Interface:     "wwan0",
			ModemIMEI:     "111111111111111",
		},
	})

	if got := idx.LookupByIMEI("222222222222222"); got != "" {
		t.Fatalf("LookupByIMEI(different) = %q, want empty", got)
	}
	if got := idx.LookupByIMEI("111111111111111"); got != "old-device" {
		t.Fatalf("LookupByIMEI(same) = %q, want old-device", got)
	}
	if got := idx.LookupByStaticPath("/dev/cdc-wdm0", "", ""); got != "old-device" {
		t.Fatalf("LookupByStaticPath(control) = %q, want old-device", got)
	}
	if got := idx.LookupByStaticPath("", "/sys/bus/usb/devices/1-1", ""); got != "old-device" {
		t.Fatalf("LookupByStaticPath(usb) = %q, want old-device", got)
	}
	if got := idx.LookupByStaticPath("", "", "wwan0"); got != "old-device" {
		t.Fatalf("LookupByStaticPath(iface) = %q, want old-device", got)
	}
}

func TestConfiguredDeviceIndexLookupByIMEINormalizes(t *testing.T) {
	idx := BuildConfiguredDeviceIndex([]config.DeviceConfig{
		{ID: "ec20-1", ModemIMEI: "864388041069422"},
	})

	if id := idx.LookupByIMEI(" 864388041069422"); id != "ec20-1" {
		t.Fatalf("whitespace-differing lookup = %q, want ec20-1", id)
	}
	if id := idx.LookupByIMEI("8643880410694201"); id != "ec20-1" {
		t.Fatalf("IMEISV lookup = %q, want ec20-1", id)
	}
	if id := idx.LookupByIMEI("864513045234397"); id != "" {
		t.Fatalf("different modem lookup = %q, want empty", id)
	}
	if id := idx.LookupByIMEI("123"); id != "" {
		t.Fatalf("invalid lookup = %q, want empty", id)
	}
}

func TestBuildWorkerDiscoveryIndexIncludesRuntimeStatus(t *testing.T) {
	worker := &Worker{
		ID: "dev-live",
		Config: config.DeviceConfig{
			ID:            "dev-live",
			ControlDevice: "/dev/cdc-wdm5",
			USBPath:       "/sys/bus/usb/devices/5-1",
			Interface:     "wwan5",
			ATPort:        "/dev/ttyUSB9",
			ModemIMEI:     "config-imei",
		},
		Backend: &workerStatusBackendStub{mode: "qmi"},
	}

	idx := BuildWorkerDiscoveryIndex([]*Worker{worker}, true)
	info, ok := idx.Lookup("/dev/cdc-wdm5", "/sys/bus/usb/devices/5-1", "wwan5")
	if !ok {
		t.Fatal("expected live worker lookup to succeed")
	}
	if info.ID != "dev-live" {
		t.Fatalf("ID=%q want dev-live", info.ID)
	}
	if info.ATPort != "/dev/ttyUSB9" {
		t.Fatalf("ATPort=%q want /dev/ttyUSB9", info.ATPort)
	}
	if info.IMEI != "config-imei" {
		t.Fatalf("IMEI=%q want config-imei", info.IMEI)
	}
	if info.USBNetMode == nil || *info.USBNetMode != 0 {
		t.Fatalf("USBNetMode=%v want pointer to 0", info.USBNetMode)
	}
}
