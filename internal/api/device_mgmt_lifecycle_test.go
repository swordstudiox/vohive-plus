package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/device"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func TestApplyLifecycleToOfflineOverviewItemKeepsRecoveryVisible(t *testing.T) {
	pool := device.NewPool(&config.Config{})
	pool.MarkLifecycleRecovery("dev1", device.LifecyclePhaseUSBWait, "manual_reboot", 3*time.Minute)
	server := &Server{pool: pool}

	item := deviceMgmtOverviewLiteItem{
		ID:            "dev1",
		Running:       false,
		Healthy:       false,
		ControlOnline: false,
	}

	server.applyLifecycleToOverviewLiteItem(&item, nil, config.DeviceConfig{ID: "dev1"})

	if item.LifecyclePhase != string(device.LifecyclePhaseUSBWait) {
		t.Fatalf("LifecyclePhase=%q want %q", item.LifecyclePhase, device.LifecyclePhaseUSBWait)
	}
	if !item.PhysicalPresent {
		t.Fatal("PhysicalPresent=false want true during active recovery")
	}
	if item.WorkerRunning {
		t.Fatal("WorkerRunning=true want false")
	}
}

func TestApplyLifecycleToListItemDerivesRadioRegistered(t *testing.T) {
	pool := device.NewPool(&config.Config{})
	pool.MarkLifecycleRecovery("dev1", device.LifecyclePhaseQMIStarting, "startup", 3*time.Minute)
	server := &Server{pool: pool}

	item := deviceMgmtListItem{
		ID:               "dev1",
		Running:          true,
		Healthy:          true,
		ControlOnline:    true,
		NetworkConnected: true,
		Modem:            deviceMgmtListModem{RegStatus: 5},
	}

	server.applyLifecycleToListItem(&item, true, config.DeviceConfig{ID: "dev1"})

	if !item.RadioRegistered {
		t.Fatal("RadioRegistered=false want true for reg status 5")
	}
	if !item.DataConnected {
		t.Fatal("DataConnected=false want true when network is connected")
	}
	if item.LifecyclePhase != string(device.LifecyclePhaseOnline) {
		t.Fatalf("LifecyclePhase=%q want %q", item.LifecyclePhase, device.LifecyclePhaseOnline)
	}
}

func TestVoWiFiRuntimeDTOExportsSIMReadyOnly(t *testing.T) {
	dto := runtimeStateToDTO(runtimehost.State{
		Phase:      runtimehost.PhaseSIMReady,
		DeviceID:   "dev1",
		SIMReady:   true,
		LastReason: "sim_ready",
	}, modem.DeviceStatus{IMSI: "001010123456789"})

	if !dto.SIMReady {
		t.Fatalf("SIMReady=false in dto=%+v", dto)
	}
	body, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("Marshal() err=%v", err)
	}
	raw := string(body)
	if !strings.Contains(raw, `"sim_ready":true`) {
		t.Fatalf("json=%s, want sim_ready=true", raw)
	}
	if strings.Contains(raw, "radio_ready") {
		t.Fatalf("json=%s should not contain radio_ready", raw)
	}
}
