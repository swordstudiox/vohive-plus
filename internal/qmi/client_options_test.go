package qmicore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestClientOptionsFromDeviceConfigKeepsRuntimeDefaultsAndProxy(t *testing.T) {
	proxyExecutable := filepath.Join(t.TempDir(), "qmi-proxy")
	writeExecutableForTest(t, proxyExecutable)

	opts := ClientOptionsFromDeviceConfig(config.DeviceConfig{
		QMIUseProxy:        true,
		QMIProxyPath:       "custom-qmi-proxy",
		QMIProxyExecutable: proxyExecutable,
	})

	if !opts.UseProxy {
		t.Fatal("UseProxy=false, want true")
	}
	if opts.ProxyFallbackToRaw {
		t.Fatal("ProxyFallbackToRaw=true, want false for explicit proxy mode")
	}
	if opts.ProxyPath != "custom-qmi-proxy" {
		t.Fatalf("ProxyPath=%q, want custom-qmi-proxy", opts.ProxyPath)
	}
	if opts.ProxyExecutable != proxyExecutable {
		t.Fatalf("ProxyExecutable=%q, want %s", opts.ProxyExecutable, proxyExecutable)
	}
	if !opts.SyncOnOpen {
		t.Fatal("SyncOnOpen=false, want true")
	}
	if opts.ReadDeadline != 100*time.Millisecond {
		t.Fatalf("ReadDeadline=%s, want 100ms", opts.ReadDeadline)
	}
	if opts.DefaultRequestTimeout != 30*time.Second {
		t.Fatalf("DefaultRequestTimeout=%s, want 30s", opts.DefaultRequestTimeout)
	}
	if opts.TxQueueSize != 128 {
		t.Fatalf("TxQueueSize=%d, want 128", opts.TxQueueSize)
	}
	if opts.IndicationQueueSize != 256 {
		t.Fatalf("IndicationQueueSize=%d, want 256", opts.IndicationQueueSize)
	}
}

func TestClientOptionsFromDeviceConfigDefaultsQMIBackendToDirectWhenControlDeviceUnused(t *testing.T) {
	proxyExecutable := filepath.Join(t.TempDir(), "qmi-proxy")
	writeExecutableForTest(t, proxyExecutable)
	restore := stubQMIControlDeviceHolders(t, qmiControlDeviceHolders{})
	defer restore()

	opts := ClientOptionsFromDeviceConfig(config.DeviceConfig{
		DeviceBackend:      "qmi",
		ControlDevice:      "/dev/cdc-wdm0",
		QMIProxyExecutable: proxyExecutable,
	})

	if opts.UseProxy {
		t.Fatal("UseProxy=true, want false for unused qmi control device")
	}
	if opts.ProxyFallbackToRaw {
		t.Fatal("ProxyFallbackToRaw=true, want no raw fallback in auto direct mode")
	}
}

func TestClientOptionsFromDeviceConfigUsesProxyForQMIBackendWhenControlDeviceHasHolders(t *testing.T) {
	proxyExecutable := filepath.Join(t.TempDir(), "qmi-proxy")
	writeExecutableForTest(t, proxyExecutable)
	restore := stubQMIControlDeviceHolders(t, qmiControlDeviceHolders{
		Holders: []qmiControlDeviceHolder{{PID: 1234, Command: "qmi-proxy"}},
	})
	defer restore()

	opts := ClientOptionsFromDeviceConfig(config.DeviceConfig{
		DeviceBackend:      "qmi",
		ControlDevice:      "/dev/cdc-wdm0",
		QMIProxyExecutable: proxyExecutable,
	})

	if !opts.UseProxy {
		t.Fatal("UseProxy=false, want true when qmi control device has holders")
	}
	if opts.ProxyFallbackToRaw {
		t.Fatal("ProxyFallbackToRaw=true, want no raw fallback when holders are present")
	}
}

func TestClientOptionsFromDeviceConfigUsesProxyWhenHolderScanUnknown(t *testing.T) {
	proxyExecutable := filepath.Join(t.TempDir(), "qmi-proxy")
	writeExecutableForTest(t, proxyExecutable)
	restore := stubQMIControlDeviceHolders(t, qmiControlDeviceHolders{Unknown: true})
	defer restore()

	opts := ClientOptionsFromDeviceConfig(config.DeviceConfig{
		DeviceBackend:      "qmi",
		ControlDevice:      "/dev/cdc-wdm0",
		QMIProxyExecutable: proxyExecutable,
	})

	if !opts.UseProxy {
		t.Fatal("UseProxy=false, want true when qmi holder scan is unknown")
	}
	if opts.ProxyFallbackToRaw {
		t.Fatal("ProxyFallbackToRaw=true, want no raw fallback when scan is unknown")
	}
}

func TestDiscoveryClientOptionsForControlDeviceSkipsWhenHeldByNonProxyProcess(t *testing.T) {
	restore := stubQMIControlDeviceHolders(t, qmiControlDeviceHolders{
		Holders: []qmiControlDeviceHolder{{PID: 4321, Command: "vohive"}},
	})
	defer restore()

	opts, ok := DiscoveryClientOptionsForControlDevice("/dev/cdc-wdm0")
	if ok {
		t.Fatal("ok=true, want false when control device is already held by a non-proxy process")
	}
	if opts.UseProxy {
		t.Fatal("UseProxy=true, want false because caller must skip optional discovery probe")
	}
	if opts.ProxyFallbackToRaw {
		t.Fatal("ProxyFallbackToRaw=true, want false")
	}
}

func TestDiscoveryClientOptionsForControlDeviceUsesProxyWhenOnlyProxyHoldsDevice(t *testing.T) {
	restore := stubQMIControlDeviceHolders(t, qmiControlDeviceHolders{
		Holders: []qmiControlDeviceHolder{{PID: 1234, Command: "/usr/libexec/qmi-proxy"}},
	})
	defer restore()

	opts, ok := DiscoveryClientOptionsForControlDevice("/dev/cdc-wdm0")
	if !ok {
		t.Fatal("ok=false, want true when qmi-proxy is the only holder")
	}
	if !opts.UseProxy {
		t.Fatal("UseProxy=false, want true when qmi-proxy is the only holder")
	}
	if opts.ProxyFallbackToRaw {
		t.Fatal("ProxyFallbackToRaw=true, want false")
	}
}

func TestClientOptionsFromDeviceConfigKeepsATBackendRawByDefault(t *testing.T) {
	opts := ClientOptionsFromDeviceConfig(config.DeviceConfig{
		DeviceBackend: "at",
	})

	if opts.UseProxy {
		t.Fatal("UseProxy=true, want false for at backend")
	}
	if opts.ProxyFallbackToRaw {
		t.Fatal("ProxyFallbackToRaw=true, want false for at backend")
	}
}

func TestClientOpenModeSummaryReportsProxy(t *testing.T) {
	proxyExecutable := filepath.Join(t.TempDir(), "qmi-proxy")
	writeExecutableForTest(t, proxyExecutable)
	restore := stubQMIControlDeviceHolders(t, qmiControlDeviceHolders{
		Holders: []qmiControlDeviceHolder{{PID: 1234, Command: "qmi-proxy"}},
	})
	defer restore()

	fields := clientOpenModeSummary(config.DeviceConfig{
		ID:                 "dev-qmi",
		ControlDevice:      "/dev/cdc-wdm0",
		DeviceBackend:      "qmi",
		QMIProxyPath:       "@qmi-proxy",
		QMIProxyExecutable: proxyExecutable,
	})
	got := fieldsToMap(fields)

	if got["device"] != "dev-qmi" {
		t.Fatalf("device=%v, want dev-qmi", got["device"])
	}
	if got["control_device"] != "/dev/cdc-wdm0" {
		t.Fatalf("control_device=%v, want /dev/cdc-wdm0", got["control_device"])
	}
	if got["qmi_use_proxy"] != true {
		t.Fatalf("qmi_use_proxy=%v, want true", got["qmi_use_proxy"])
	}
	if got["qmi_transport_selected"] != "proxy" {
		t.Fatalf("qmi_transport_selected=%v, want proxy", got["qmi_transport_selected"])
	}
	if got["qmi_control_holder_count"] != 1 {
		t.Fatalf("qmi_control_holder_count=%v, want 1", got["qmi_control_holder_count"])
	}
	if got["qmi_proxy_fallback_to_raw"] != false {
		t.Fatalf("qmi_proxy_fallback_to_raw=%v, want false", got["qmi_proxy_fallback_to_raw"])
	}
	if got["qmi_proxy_path"] != "@qmi-proxy" {
		t.Fatalf("qmi_proxy_path=%v, want @qmi-proxy", got["qmi_proxy_path"])
	}
	if got["qmi_proxy_executable"] != proxyExecutable {
		t.Fatalf("qmi_proxy_executable=%v, want %s", got["qmi_proxy_executable"], proxyExecutable)
	}
}

func TestClientOpenModeSummaryReportsRawMode(t *testing.T) {
	fields := clientOpenModeSummary(config.DeviceConfig{
		ID:            "dev-qmi",
		ControlDevice: "/dev/cdc-wdm0",
	})
	got := fieldsToMap(fields)

	if got["control_device"] != "/dev/cdc-wdm0" {
		t.Fatalf("control_device=%v, want /dev/cdc-wdm0", got["control_device"])
	}
	if got["qmi_use_proxy"] != false {
		t.Fatalf("qmi_use_proxy=%v, want false", got["qmi_use_proxy"])
	}
	if _, ok := got["qmi_proxy_path"]; ok {
		t.Fatalf("qmi_proxy_path present in raw summary: %v", got["qmi_proxy_path"])
	}
	if _, ok := got["qmi_proxy_executable"]; ok {
		t.Fatalf("qmi_proxy_executable present in raw summary: %v", got["qmi_proxy_executable"])
	}
}

func fieldsToMap(fields []any) map[string]any {
	out := make(map[string]any, len(fields)/2)
	for i := 0; i+1 < len(fields); i += 2 {
		key, _ := fields[i].(string)
		out[key] = fields[i+1]
	}
	return out
}

func writeExecutableForTest(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write proxy executable: %v", err)
	}
}

func stubQMIControlDeviceHolders(t *testing.T, scan qmiControlDeviceHolders) func() {
	t.Helper()
	orig := detectQMIControlDeviceHolders
	detectQMIControlDeviceHolders = func(path string) (qmiControlDeviceHolders, error) {
		if path != "/dev/cdc-wdm0" {
			t.Fatalf("holder scan path=%q, want /dev/cdc-wdm0", path)
		}
		return scan, nil
	}
	return func() {
		detectQMIControlDeviceHolders = orig
	}
}
