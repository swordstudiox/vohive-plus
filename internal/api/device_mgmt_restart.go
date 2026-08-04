package api

import "github.com/swordstudiox/vohive-plus/internal/config"

func deviceConfigRequiresRestart(old config.DeviceConfig, next config.DeviceConfig) bool {
	if config.NormalizeIMEI(old.ModemIMEI) != config.NormalizeIMEI(next.ModemIMEI) {
		return true
	}
	if old.USBPath != next.USBPath {
		return true
	}
	if old.Interface != next.Interface {
		return true
	}
	if old.ProxyPort != next.ProxyPort {
		return true
	}
	if old.ATPort != next.ATPort {
		return true
	}
	if old.ControlDevice != next.ControlDevice {
		return true
	}
	if config.NormalizeESIMTransport(old.ESIMTransport) != config.NormalizeESIMTransport(next.ESIMTransport) {
		return true
	}
	if old.BaudRate != next.BaudRate {
		return true
	}
	if old.DataBits != next.DataBits {
		return true
	}
	if old.StopBits != next.StopBits {
		return true
	}
	if old.Parity != next.Parity {
		return true
	}
	if old.APN != next.APN {
		return true
	}
	if old.IPVersion != next.IPVersion {
		return true
	}
	// 后端模式变更（at↔qmi↔auto）需要重建 Worker
	if old.DeviceBackend != next.DeviceBackend {
		return true
	}
	if qmiProxyConfigChanged(old, next) {
		return true
	}
	return false
}

func qmiProxyConfigChanged(old config.DeviceConfig, next config.DeviceConfig) bool {
	if old.QMIUseProxy != next.QMIUseProxy {
		return true
	}
	if old.QMIProxyPath != next.QMIProxyPath {
		return true
	}
	if old.QMIProxyExecutable != next.QMIProxyExecutable {
		return true
	}
	return false
}

func managedNetworkConfigChanged(old, next config.DeviceConfig) bool {
	return old.APN != next.APN ||
		old.Interface != next.Interface ||
		old.ControlDevice != next.ControlDevice ||
		old.IPVersion != next.IPVersion
}
