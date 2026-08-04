package api

import (
	"context"
	"strings"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/device"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func TestFlightModeSuccessMessageUsesRequestedState(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		want    string
	}{
		{name: "enable", enabled: true, want: "飞行模式已开启"},
		{name: "disable", enabled: false, want: "飞行模式已关闭"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := flightModeSuccessMessage(tt.enabled); got != tt.want {
				t.Fatalf("flightModeSuccessMessage(%v)=%q want %q", tt.enabled, got, tt.want)
			}
		})
	}
}

func TestSetWorkerFlightModeFailsWhenBackendMissingEvenWithATModem(t *testing.T) {
	m, err := modem.New(config.DeviceConfig{
		ID:            "dev-qmi",
		DeviceBackend: "qmi",
		ATPort:        "/dev/ttyUSB-test",
	})
	if err != nil {
		t.Fatalf("modem.New() error = %v", err)
	}

	_, _, err = setWorkerFlightMode(context.Background(), &device.Worker{Modem: m}, false)
	if err == nil {
		t.Fatal("setWorkerFlightMode() error = nil, want backend initialization error")
	}
	if !strings.Contains(err.Error(), "设备后端未初始化") {
		t.Fatalf("setWorkerFlightMode() error = %q, want backend initialization error", err.Error())
	}
	if strings.Contains(err.Error(), "AT 管理器") || strings.Contains(err.Error(), "AT 端口") {
		t.Fatalf("setWorkerFlightMode() error = %q, must not come from legacy AT fallback", err.Error())
	}
}
