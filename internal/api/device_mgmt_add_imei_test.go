package api

import (
	"errors"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestEnsureAddDeviceIMEIBackfillsWhenEmpty(t *testing.T) {
	cfg := config.DeviceConfig{ID: "ec20-1", ControlDevice: "/dev/cdc-wdm2"}
	probe := func(controlPath string) (string, error) {
		if controlPath != "/dev/cdc-wdm2" {
			t.Fatalf("probe got %q", controlPath)
		}
		return "864388041069422", nil
	}

	got, err := ensureAddDeviceIMEI(cfg, probe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ModemIMEI != "864388041069422" {
		t.Fatalf("ModemIMEI = %q, want probed value", got.ModemIMEI)
	}
}

func TestEnsureAddDeviceIMEIRejectsWhenProbeFails(t *testing.T) {
	cfg := config.DeviceConfig{ID: "ec20-1", ControlDevice: "/dev/cdc-wdm2"}
	probe := func(string) (string, error) { return "", errors.New("timeout") }

	if _, err := ensureAddDeviceIMEI(cfg, probe); err == nil {
		t.Fatal("expected error when probe fails, got nil")
	}
}

func TestEnsureAddDeviceIMEIRejectsWhenProbeEmpty(t *testing.T) {
	cfg := config.DeviceConfig{ID: "ec20-1", ControlDevice: "/dev/cdc-wdm2"}
	probe := func(string) (string, error) { return "  ", nil }

	if _, err := ensureAddDeviceIMEI(cfg, probe); err == nil {
		t.Fatal("expected error when probe returns blank, got nil")
	}
}

func TestEnsureAddDeviceIMEILeavesExistingIMEIAndATOnly(t *testing.T) {
	withIMEI := config.DeviceConfig{ID: "ec20-1", ControlDevice: "/dev/cdc-wdm2", ModemIMEI: "864388041069422"}
	probeFail := func(string) (string, error) {
		t.Fatal("probe must not run when IMEI present")
		return "", nil
	}
	if got, err := ensureAddDeviceIMEI(withIMEI, probeFail); err != nil || got.ModemIMEI != "864388041069422" {
		t.Fatalf("with-IMEI: got %+v err %v", got, err)
	}

	atOnly := config.DeviceConfig{ID: "at-1", ATPort: "/dev/ttyUSB2"}
	if _, err := ensureAddDeviceIMEI(atOnly, probeFail); err != nil {
		t.Fatalf("AT-only must not be enforced: %v", err)
	}
}
