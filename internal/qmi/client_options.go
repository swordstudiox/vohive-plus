package qmicore

import (
	"strings"

	qmiq "github.com/iniwex5/quectel-qmi-go/pkg/qmi"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func ClientOptionsFromDeviceConfig(cfg config.DeviceConfig) qmiq.ClientOptions {
	opts, _ := clientOptionsFromDeviceConfig(cfg)
	return opts
}

func clientOptionsFromDeviceConfig(cfg config.DeviceConfig) (qmiq.ClientOptions, qmiTransportDecision) {
	opts := qmiq.DefaultClientOptions()
	backend := strings.ToLower(strings.TrimSpace(cfg.DeviceBackend))
	if proxyPath := strings.TrimSpace(cfg.QMIProxyPath); proxyPath != "" {
		opts.ProxyPath = proxyPath
	}
	if proxyExecutable := strings.TrimSpace(cfg.QMIProxyExecutable); proxyExecutable != "" {
		opts.ProxyExecutable = proxyExecutable
	}
	decision := decideQMITransport(cfg, backend)
	applyQMITransportDecision(&opts, decision)
	return opts, decision
}

func DiscoveryClientOptionsForControlDevice(controlDevice string) (qmiq.ClientOptions, bool) {
	opts := qmiq.DefaultClientOptions()
	controlDevice = strings.TrimSpace(controlDevice)
	if controlDevice == "" {
		return opts, true
	}
	holders, err := detectQMIControlDeviceHolders(controlDevice)
	if err != nil || holders.Unknown {
		forceProxy(&opts)
		return opts, true
	}
	if len(holders.Holders) == 0 {
		return opts, true
	}
	if holders.onlyQMIProxy() {
		forceProxy(&opts)
		return opts, true
	}
	return opts, false
}

func clientOpenModeSummary(cfg config.DeviceConfig) []any {
	opts, decision := clientOptionsFromDeviceConfig(cfg)
	controlDevice := strings.TrimSpace(cfg.ControlDevice)
	if controlDevice == "" {
		controlDevice = strings.TrimSpace(cfg.QMIDevice)
	}

	fields := []any{
		"device", strings.TrimSpace(cfg.ID),
		"control_device", controlDevice,
		"qmi_use_proxy", opts.UseProxy,
		"qmi_transport_selected", qmiTransportName(opts.UseProxy),
	}
	if decision.ControlDeviceScanned {
		if decision.HolderScanError != "" {
			fields = append(fields, "qmi_control_holder_scan_error", decision.HolderScanError)
		} else {
			fields = append(fields,
				"qmi_control_holder_count", decision.HolderCount,
				"qmi_control_holder_scan_unknown", decision.HolderScanUnknown)
		}
	}
	if opts.UseProxy {
		fields = append(fields,
			"qmi_proxy_fallback_to_raw", opts.ProxyFallbackToRaw,
			"qmi_proxy_path", opts.ProxyPath,
			"qmi_proxy_executable", opts.ProxyExecutable,
		)
	}
	return fields
}

func qmiTransportName(useProxy bool) string {
	if useProxy {
		return "proxy"
	}
	return "direct"
}

type qmiTransportDecision struct {
	UseProxy             bool
	ExplicitProxy        bool
	ControlDeviceScanned bool
	HolderCount          int
	HolderScanUnknown    bool
	HolderScanError      string
}

func decideQMITransport(cfg config.DeviceConfig, backend string) qmiTransportDecision {
	decision := qmiTransportDecision{}
	controlDevice := strings.TrimSpace(cfg.ControlDevice)
	if controlDevice == "" {
		controlDevice = strings.TrimSpace(cfg.QMIDevice)
	}
	if controlDevice != "" {
		decision.ControlDeviceScanned = true
		holders, err := detectQMIControlDeviceHolders(controlDevice)
		if err != nil {
			decision.HolderScanError = err.Error()
		} else {
			decision.HolderCount = len(holders.Holders)
			decision.HolderScanUnknown = holders.Unknown
		}
	}

	if cfg.QMIUseProxy {
		decision.UseProxy = true
		decision.ExplicitProxy = true
		return decision
	}
	if backend == "qmi" &&
		decision.ControlDeviceScanned &&
		(decision.HolderScanError != "" || decision.HolderScanUnknown || decision.HolderCount > 0) {
		decision.UseProxy = true
	}
	return decision
}

func applyQMITransportDecision(opts *qmiq.ClientOptions, decision qmiTransportDecision) {
	if opts == nil {
		return
	}
	if decision.UseProxy {
		forceProxy(opts)
		opts.ProxyFallbackToRaw = !decision.ExplicitProxy
		return
	}
	opts.UseProxy = false
	opts.ProxyFallbackToRaw = false
}

func forceProxy(opts *qmiq.ClientOptions) {
	if opts == nil {
		return
	}
	opts.UseProxy = true
	opts.ProxyFallbackToRaw = false
}
