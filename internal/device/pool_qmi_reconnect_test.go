package device

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestQMIManagedAttachmentChanged(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.DeviceConfig
		dev  QMIDevice
		want bool
	}{
		{
			name: "same attachment",
			cfg: config.DeviceConfig{
				ControlDevice: "/dev/cdc-wdm1",
				Interface:     "wwan0",
				USBPath:       "/sys/bus/usb/devices/1-9",
			},
			dev: QMIDevice{
				ControlPath:  "/dev/cdc-wdm1",
				NetInterface: "wwan0",
				USBPath:      "/sys/bus/usb/devices/1-9",
			},
			want: false,
		},
		{
			name: "control path changed",
			cfg: config.DeviceConfig{
				ControlDevice: "/dev/cdc-wdm1",
				Interface:     "wwan0",
				USBPath:       "/sys/bus/usb/devices/1-9",
			},
			dev: QMIDevice{
				ControlPath:  "/dev/cdc-wdm0",
				NetInterface: "wwan0",
				USBPath:      "/sys/bus/usb/devices/1-9",
			},
			want: true,
		},
		{
			name: "interface changed",
			cfg: config.DeviceConfig{
				ControlDevice: "/dev/cdc-wdm1",
				Interface:     "wwan0",
				USBPath:       "/sys/bus/usb/devices/1-9",
			},
			dev: QMIDevice{
				ControlPath:  "/dev/cdc-wdm1",
				NetInterface: "wwan1",
				USBPath:      "/sys/bus/usb/devices/1-9",
			},
			want: true,
		},
		{
			name: "usb path changed",
			cfg: config.DeviceConfig{
				ControlDevice: "/dev/cdc-wdm1",
				Interface:     "wwan0",
				USBPath:       "/sys/bus/usb/devices/1-9",
			},
			dev: QMIDevice{
				ControlPath:  "/dev/cdc-wdm1",
				NetInterface: "wwan0",
				USBPath:      "/sys/bus/usb/devices/1-10",
			},
			want: true,
		},
		{
			name: "empty discovered fields do not force rebuild",
			cfg: config.DeviceConfig{
				ControlDevice: "/dev/cdc-wdm1",
				Interface:     "wwan0",
				USBPath:       "/sys/bus/usb/devices/1-9",
			},
			dev:  QMIDevice{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qmiManagedAttachmentChanged(tt.cfg, tt.dev); got != tt.want {
				t.Fatalf("qmiManagedAttachmentChanged() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyQMIManagedAttachment(t *testing.T) {
	cfg := config.DeviceConfig{
		ID:            "wwan0",
		ModemIMEI:     "864819055348922",
		ControlDevice: "/dev/cdc-wdm1",
		Interface:     "wwan0",
		USBPath:       "/sys/bus/usb/devices/1-9",
		ATPort:        "/dev/ttyUSB2",
		AudioDevice:   "hw:1,0",
		DeviceBackend: "qmi",
	}
	dev := QMIDevice{
		ControlPath:  "/dev/cdc-wdm0",
		NetInterface: "wwan1",
		USBPath:      "/sys/bus/usb/devices/1-10",
		ATPort:       "/dev/ttyUSB3",
		AudioDevice:  "hw:2,0",
	}

	got := applyQMIManagedAttachment(cfg, dev)

	if got.ControlDevice != "/dev/cdc-wdm0" {
		t.Fatalf("ControlDevice = %q, want /dev/cdc-wdm0", got.ControlDevice)
	}
	if got.Interface != "wwan1" {
		t.Fatalf("Interface = %q, want wwan1", got.Interface)
	}
	if got.USBPath != "/sys/bus/usb/devices/1-10" {
		t.Fatalf("USBPath = %q, want /sys/bus/usb/devices/1-10", got.USBPath)
	}
	if got.ATPort != "/dev/ttyUSB3" {
		t.Fatalf("ATPort = %q, want /dev/ttyUSB3", got.ATPort)
	}
	if got.QMIDevice != "/dev/cdc-wdm0" {
		t.Fatalf("QMIDevice = %q, want /dev/cdc-wdm0", got.QMIDevice)
	}
	if got.ManagePort != "/dev/ttyUSB3" {
		t.Fatalf("ManagePort = %q, want /dev/ttyUSB3", got.ManagePort)
	}
	if got.AudioDevice != "hw:2,0" {
		t.Fatalf("AudioDevice = %q, want hw:2,0", got.AudioDevice)
	}
	if got.ModemIMEI != "864819055348922" || got.DeviceBackend != "qmi" {
		t.Fatalf("stable fields changed unexpectedly: %#v", got)
	}

	dev.AudioDevice = ""
	got = applyQMIManagedAttachment(cfg, dev)
	if got.AudioDevice != "hw:1,0" {
		t.Fatalf("AudioDevice after empty live value = %q, want hw:1,0", got.AudioDevice)
	}
}

func TestQMIHealthyWorkerNeedsRebuildWhenManagedAttachmentChanges(t *testing.T) {
	worker := &Worker{
		ID: "wwan0",
		Config: config.DeviceConfig{
			ID:            "wwan0",
			ModemIMEI:     "864819055348922",
			ControlDevice: "/dev/cdc-wdm1",
			Interface:     "wwan0",
			USBPath:       "/sys/bus/usb/devices/1-9",
			ATPort:        "/dev/ttyUSB2",
			DeviceBackend: "qmi",
		},
	}
	live := QMIDevice{
		ControlPath:  "/dev/cdc-wdm0",
		NetInterface: "wwan1",
		USBPath:      "/sys/bus/usb/devices/1-10",
		ATPort:       "/dev/ttyUSB2",
	}

	changed, nextCfg := qmiHealthyWorkerAttachmentUpdate(worker, live)

	if !changed {
		t.Fatal("qmiHealthyWorkerAttachmentUpdate changed = false, want true")
	}
	if nextCfg.ControlDevice != "/dev/cdc-wdm0" || nextCfg.Interface != "wwan1" || nextCfg.USBPath != "/sys/bus/usb/devices/1-10" {
		t.Fatalf("next config did not adopt live QMI attachment: %#v", nextCfg)
	}
	if nextCfg.ATPort != "/dev/ttyUSB2" {
		t.Fatalf("ATPort = %q, want unchanged /dev/ttyUSB2", nextCfg.ATPort)
	}
}

func TestQMIHealthyWorkerDoesNotNeedRebuildForSameAttachment(t *testing.T) {
	worker := &Worker{
		ID: "wwan0",
		Config: config.DeviceConfig{
			ID:            "wwan0",
			ModemIMEI:     "864819055348922",
			ControlDevice: "/dev/cdc-wdm1",
			Interface:     "wwan0",
			USBPath:       "/sys/bus/usb/devices/1-9",
			ATPort:        "/dev/ttyUSB2",
			DeviceBackend: "qmi",
		},
	}
	live := QMIDevice{
		ControlPath:  "/dev/cdc-wdm1",
		NetInterface: "wwan0",
		USBPath:      "/sys/bus/usb/devices/1-9",
		ATPort:       "/dev/ttyUSB2",
	}

	changed, _ := qmiHealthyWorkerAttachmentUpdate(worker, live)

	if changed {
		t.Fatal("qmiHealthyWorkerAttachmentUpdate changed = true, want false")
	}
}

func TestQMIHealthyWorkerAttachmentUpdateSkipsInvalidWorkers(t *testing.T) {
	live := QMIDevice{
		ControlPath:  "/dev/cdc-wdm0",
		NetInterface: "wwan1",
		USBPath:      "/sys/bus/usb/devices/1-10",
	}

	changed, _ := qmiHealthyWorkerAttachmentUpdate(nil, live)
	if changed {
		t.Fatal("nil worker changed = true, want false")
	}

	worker := &Worker{
		ID: "at-device",
		Config: config.DeviceConfig{
			ID:            "at-device",
			ATPort:        "/dev/ttyUSB2",
			DeviceBackend: "at",
		},
	}
	changed, _ = qmiHealthyWorkerAttachmentUpdate(worker, live)
	if changed {
		t.Fatal("non-QMI worker changed = true, want false")
	}
}

func TestStartAllIMEIMissAllowsConfiguredStaticQMIAttachment(t *testing.T) {
	cfg := config.DeviceConfig{
		ID:            "wwan0",
		ModemIMEI:     "864819055348922",
		ControlDevice: "/dev/cdc-wdm0",
		Interface:     "wwan0",
		USBPath:       "/sys/bus/usb/devices/1-9",
		DeviceBackend: "qmi",
	}
	idx := BuildStaticQMIDeviceIndex([]QMIDevice{{
		ControlPath:  "/dev/cdc-wdm0",
		NetInterface: "wwan0",
		USBPath:      "/sys/bus/usb/devices/1-9",
	}})

	if !shouldStartConfiguredQMIWithoutIMEIMatch(cfg, idx, true) {
		t.Fatal("configured static QMI attachment should start when IMEI probe is temporarily unavailable")
	}
}

func TestStartAllIMEIMissRejectsStaleStaticQMIAttachment(t *testing.T) {
	cfg := config.DeviceConfig{
		ID:            "wwan0",
		ModemIMEI:     "864819055348922",
		ControlDevice: "/dev/cdc-wdm0",
		Interface:     "wwan0",
		USBPath:       "/sys/bus/usb/devices/1-9",
		DeviceBackend: "qmi",
	}
	idx := BuildStaticQMIDeviceIndex([]QMIDevice{{
		ControlPath:  "/dev/cdc-wdm1",
		NetInterface: "wwan1",
		USBPath:      "/sys/bus/usb/devices/1-10",
	}})

	if shouldStartConfiguredQMIWithoutIMEIMatch(cfg, idx, true) {
		t.Fatal("stale static QMI attachment should not start when discovery has a different live device")
	}
}

func TestStartAllIMEIMissAllowsStaticQMIWhenDiscoveryUnavailable(t *testing.T) {
	cfg := config.DeviceConfig{
		ID:            "wwan0",
		ModemIMEI:     "864819055348922",
		ControlDevice: "/dev/cdc-wdm0",
		DeviceBackend: "qmi",
	}

	if !shouldStartConfiguredQMIWithoutIMEIMatch(cfg, StaticQMIDeviceIndex{}, false) {
		t.Fatal("static QMI config should start when discovery is unavailable")
	}
}

func TestQMIBootstrapDiscoveryCacheReusesFirstResult(t *testing.T) {
	orig := discoverQMIDevicesFn
	t.Cleanup(func() {
		discoverQMIDevicesFn = orig
	})

	calls := 0
	discoverQMIDevicesFn = func() ([]QMIDevice, error) {
		calls++
		return []QMIDevice{{ControlPath: "/dev/cdc-wdm0", NetInterface: "wwan0"}}, nil
	}

	cache := &qmiBootstrapDiscoveryCache{}
	first, err := cache.Get()
	if err != nil {
		t.Fatalf("first Get() error = %v", err)
	}
	second, err := cache.Get()
	if err != nil {
		t.Fatalf("second Get() error = %v", err)
	}

	if calls != 1 {
		t.Fatalf("discover calls = %d, want 1", calls)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ControlPath != second[0].ControlPath {
		t.Fatalf("unexpected cached discovery results: first=%#v second=%#v", first, second)
	}
}

func TestShouldFastStartMissingQMIWorker(t *testing.T) {
	controlPath := filepath.Join(t.TempDir(), "cdc-wdm-test")
	if err := os.WriteFile(controlPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.DeviceConfig{
		ID:            "wwan0",
		ModemIMEI:     "864819055348922",
		ControlDevice: controlPath,
		Interface:     "wwan0",
		USBPath:       "/sys/bus/usb/devices/1-9",
		DeviceBackend: "qmi",
	}

	tests := []struct {
		name               string
		discoveryAvailable bool
		live               QMIDevice
		want               bool
	}{
		{
			name:               "no discovery available keeps existing fast start",
			discoveryAvailable: false,
			live:               QMIDevice{},
			want:               true,
		},
		{
			name:               "discovery confirms same attachment",
			discoveryAvailable: true,
			live: QMIDevice{
				ControlPath:  controlPath,
				NetInterface: "wwan0",
				USBPath:      "/sys/bus/usb/devices/1-9",
			},
			want: true,
		},
		{
			name:               "discovery succeeds without configured attachment match",
			discoveryAvailable: true,
			live:               QMIDevice{},
			want:               false,
		},
		{
			name:               "discovery shows stale configured path",
			discoveryAvailable: true,
			live: QMIDevice{
				ControlPath:  "/dev/cdc-wdm0",
				NetInterface: "wwan1",
				USBPath:      "/sys/bus/usb/devices/1-10",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldFastStartMissingQMIWorker(cfg, tt.live, tt.discoveryAvailable); got != tt.want {
				t.Fatalf("shouldFastStartMissingQMIWorker() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRescanReconnectManualRebootScopeAllowsOnlyTargetMutation(t *testing.T) {
	opts := rescanReconnectOptions{
		targetDeviceID: "wwan1",
		manualReboot:   true,
	}

	if !opts.allowWorkerMutation("wwan1") {
		t.Fatal("manual reboot scoped rescan should allow target device mutation")
	}
	if opts.allowWorkerMutation("wwan0") {
		t.Fatal("manual reboot scoped rescan should not mutate non-target devices")
	}
}

func TestRescanReconnectDefaultScopeAllowsAnyMutation(t *testing.T) {
	var opts rescanReconnectOptions

	if !opts.allowWorkerMutation("wwan0") || !opts.allowWorkerMutation("wwan1") {
		t.Fatal("default rescan should preserve existing unrestricted mutation behavior")
	}
}
