package api

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestDeviceConfigRequiresRestartWhenQMIProxyConfigChanges(t *testing.T) {
	base := config.DeviceConfig{
		ID:                 "dev-qmi",
		ControlDevice:      "/dev/cdc-wdm0",
		DeviceBackend:      "qmi",
		QMIUseProxy:        true,
		QMIProxyPath:       "qmi-proxy",
		QMIProxyExecutable: "/usr/libexec/qmi-proxy",
	}

	tests := []struct {
		name string
		edit func(*config.DeviceConfig)
	}{
		{
			name: "use proxy toggled",
			edit: func(next *config.DeviceConfig) {
				next.QMIUseProxy = false
			},
		},
		{
			name: "proxy path changed",
			edit: func(next *config.DeviceConfig) {
				next.QMIProxyPath = "custom-qmi-proxy"
			},
		},
		{
			name: "proxy executable changed",
			edit: func(next *config.DeviceConfig) {
				next.QMIProxyExecutable = "/opt/qmi-proxy"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base
			tt.edit(&next)
			if !deviceConfigRequiresRestart(base, next) {
				t.Fatal("deviceConfigRequiresRestart()=false, want true")
			}
		})
	}
}

func TestDeviceConfigRequiresRestartIgnoresIMEIFormatOnly(t *testing.T) {
	old := config.DeviceConfig{ModemIMEI: "864388041069422"}
	next := config.DeviceConfig{ModemIMEI: " 864388041069422"}

	if deviceConfigRequiresRestart(old, next) {
		t.Fatal("format-only IMEI difference must not require restart")
	}

	if deviceConfigRequiresRestart(config.DeviceConfig{}, config.DeviceConfig{}) {
		t.Fatal("two empty IMEIs must not require restart")
	}

	diff := config.DeviceConfig{ModemIMEI: "864513045234397"}
	if !deviceConfigRequiresRestart(old, diff) {
		t.Fatal("different modem IMEI must require restart")
	}

	imeisv := config.DeviceConfig{ModemIMEI: "8643880410694201"}
	if deviceConfigRequiresRestart(old, imeisv) {
		t.Fatal("IMEISV-only IMEI difference must not require restart")
	}
}

func TestDeviceConfigMBIMManagedNetworkChangesRequiresRebuild(t *testing.T) {
	base := config.DeviceConfig{
		APN:           "internet",
		Interface:     "wwan0",
		ControlDevice: "/dev/cdc-wdm0",
		IPVersion:     "ipv4",
	}

	tests := []struct {
		name string
		edit func(*config.DeviceConfig)
	}{
		{
			name: "APN changed",
			edit: func(next *config.DeviceConfig) {
				next.APN = "fast.t-mobile.com"
			},
		},
		{
			name: "Interface changed",
			edit: func(next *config.DeviceConfig) {
				next.Interface = "wwan1"
			},
		},
		{
			name: "ControlDevice changed",
			edit: func(next *config.DeviceConfig) {
				next.ControlDevice = "/dev/cdc-wdm1"
			},
		},
		{
			name: "IPVersion changed",
			edit: func(next *config.DeviceConfig) {
				next.IPVersion = "ipv4v6"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := base
			tt.edit(&next)
			if !managedNetworkConfigChanged(base, next) {
				t.Fatalf("managedNetworkConfigChanged()=false, want true for %s", tt.name)
			}
		})
	}

	if managedNetworkConfigChanged(base, base) {
		t.Fatal("managedNetworkConfigChanged()=true for identical configs, want false")
	}
}
