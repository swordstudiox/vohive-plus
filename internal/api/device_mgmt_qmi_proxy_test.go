package api

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestDeviceConfigDTOPreservesQMIProxyFields(t *testing.T) {
	cfg := config.DeviceConfig{
		ID:                 "dev-qmi",
		QMIUseProxy:        true,
		QMIProxyPath:       "custom-qmi-proxy",
		QMIProxyExecutable: "/opt/vohive/bin/qmi-proxy",
	}

	dto := deviceConfigToDTO(cfg)
	if dto.QMIUseProxy == nil || !*dto.QMIUseProxy {
		t.Fatal("dto.QMIUseProxy=nil/false, want true")
	}
	if dto.QMIProxyPath == nil || *dto.QMIProxyPath != cfg.QMIProxyPath {
		t.Fatalf("dto.QMIProxyPath=%v, want %q", dto.QMIProxyPath, cfg.QMIProxyPath)
	}
	if dto.QMIProxyExecutable == nil || *dto.QMIProxyExecutable != cfg.QMIProxyExecutable {
		t.Fatalf("dto.QMIProxyExecutable=%v, want %q", dto.QMIProxyExecutable, cfg.QMIProxyExecutable)
	}

	roundTrip := deviceConfigFromDTO(dto)
	if !roundTrip.QMIUseProxy {
		t.Fatal("roundTrip.QMIUseProxy=false, want true")
	}
	if roundTrip.QMIProxyPath != cfg.QMIProxyPath {
		t.Fatalf("roundTrip.QMIProxyPath=%q, want %q", roundTrip.QMIProxyPath, cfg.QMIProxyPath)
	}
	if roundTrip.QMIProxyExecutable != cfg.QMIProxyExecutable {
		t.Fatalf("roundTrip.QMIProxyExecutable=%q, want %q", roundTrip.QMIProxyExecutable, cfg.QMIProxyExecutable)
	}
}

func TestDeviceConfigDTOPreservesIPVersion(t *testing.T) {
	cfg := config.DeviceConfig{
		ID:        "dev-ipv",
		IPVersion: "v4v6",
	}

	dto := deviceConfigToDTO(cfg)
	if dto.IPVersion != "v4v6" {
		t.Fatalf("dto.IPVersion=%q, want v4v6", dto.IPVersion)
	}

	roundTrip := deviceConfigFromDTO(dto)
	if roundTrip.IPVersion != "v4v6" {
		t.Fatalf("roundTrip.IPVersion=%q, want v4v6", roundTrip.IPVersion)
	}
}

func TestDeviceConfigFromDTOWithBasePreservesOmittedQMIProxyFields(t *testing.T) {
	base := config.DeviceConfig{
		ID:                 "dev-qmi",
		Name:               "old name",
		ControlDevice:      "/dev/cdc-wdm0",
		QMIUseProxy:        true,
		QMIProxyPath:       "qmi-proxy",
		QMIProxyExecutable: "/usr/libexec/qmi-proxy",
	}

	cfg := deviceConfigFromDTOWithBase(deviceConfigDTO{
		ID:            "dev-qmi",
		Name:          "new name",
		ControlDevice: "/dev/cdc-wdm0",
	}, &base)

	if cfg.Name != "new name" {
		t.Fatalf("Name=%q, want %q", cfg.Name, "new name")
	}
	if !cfg.QMIUseProxy {
		t.Fatal("QMIUseProxy=false, want preserved true")
	}
	if cfg.QMIProxyPath != base.QMIProxyPath {
		t.Fatalf("QMIProxyPath=%q, want %q", cfg.QMIProxyPath, base.QMIProxyPath)
	}
	if cfg.QMIProxyExecutable != base.QMIProxyExecutable {
		t.Fatalf("QMIProxyExecutable=%q, want %q", cfg.QMIProxyExecutable, base.QMIProxyExecutable)
	}
}

func TestDeviceConfigFromDTOWithBaseAppliesExplicitQMIProxyFields(t *testing.T) {
	base := config.DeviceConfig{
		ID:                 "dev-qmi",
		QMIUseProxy:        true,
		QMIProxyPath:       "qmi-proxy",
		QMIProxyExecutable: "/usr/libexec/qmi-proxy",
	}
	useProxy := false
	proxyPath := ""
	proxyExecutable := ""

	cfg := deviceConfigFromDTOWithBase(deviceConfigDTO{
		ID:                 "dev-qmi",
		QMIUseProxy:        &useProxy,
		QMIProxyPath:       &proxyPath,
		QMIProxyExecutable: &proxyExecutable,
	}, &base)

	if cfg.QMIUseProxy {
		t.Fatal("QMIUseProxy=true, want explicit false")
	}
	if cfg.QMIProxyPath != "" {
		t.Fatalf("QMIProxyPath=%q, want empty", cfg.QMIProxyPath)
	}
	if cfg.QMIProxyExecutable != "" {
		t.Fatalf("QMIProxyExecutable=%q, want empty", cfg.QMIProxyExecutable)
	}
}
