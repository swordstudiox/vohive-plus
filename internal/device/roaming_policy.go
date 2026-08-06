package device

import (
	"fmt"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	"github.com/swordstudiox/vohive-plus/pkg/logger"
)

func RoamingServiceATCommand(enabled bool) string {
	if enabled {
		return `AT+QCFG="roamservice",255,1`
	}
	return `AT+QCFG="roamservice",1,1`
}

func ExecuteRoamingATForWorker(w *Worker, enabled bool, timeout time.Duration) (string, error) {
	return executeATForRoaming(w, RoamingServiceATCommand(enabled), timeout)
}

func (w *Worker) ValidateDataRoamingAllowed() error {
	if w == nil {
		return fmt.Errorf("network_not_available")
	}
	if w.Config.RoamingEnabled {
		return nil
	}
	w.cacheMu.RLock()
	regStatus := w.state.Runtime.RegStatus
	w.cacheMu.RUnlock()
	if regStatus == 5 {
		return fmt.Errorf("data_roaming_disabled: 当前处于漫游注册，已关闭数据漫游")
	}
	return nil
}

func (w *Worker) StopNetworkIfDataRoamingDisallowed() error {
	if w == nil {
		return nil
	}
	if err := w.ValidateDataRoamingAllowed(); err == nil {
		return nil
	}
	nc := w.NetworkController()
	if nc == nil || !nc.IsConnected() {
		w.clearCachedIP()
		return nil
	}
	return w.StopNetwork()
}

func (w *Worker) enforceDataRoamingPolicyAfterRuntimeUpdate(reason string) {
	if err := w.StopNetworkIfDataRoamingDisallowed(); err != nil {
		logger.Warn("数据漫游已关闭但停止漫游数据网络失败", "device", w.ID, "reason", reason, "err", err)
	}
}

func executeATForRoaming(w *Worker, cmd string, timeout time.Duration) (string, error) {
	if w == nil {
		return "", fmt.Errorf("设备未找到或未运行")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if w.Backend != nil {
		switch strings.TrimSpace(w.Backend.Mode()) {
		case backend.BackendQMI, backend.BackendMBIM:
			return executeTransientRoamingAT(w.ResolvedATPort(), cmd, timeout)
		}
	}
	if w.Modem == nil {
		return executeTransientRoamingAT(w.ResolvedATPort(), cmd, timeout)
	}
	if !w.Modem.HasATPort() {
		return "", fmt.Errorf("当前设备没有可用 AT 端口")
	}
	if !w.Modem.CanExecuteAT() {
		return "", fmt.Errorf("AT 管理器未启动或不可用")
	}
	return w.Modem.ExecuteAT(cmd, timeout)
}

func executeTransientRoamingAT(port, cmd string, timeout time.Duration) (string, error) {
	port = strings.TrimSpace(port)
	if port == "" {
		return "", fmt.Errorf("当前设备没有可用 AT 端口")
	}
	session, err := modem.NewSerialAT(port, 115200, 8, 1, "N")
	if err != nil {
		return "", fmt.Errorf("打开 AT 端口 %s 失败: %w", port, err)
	}
	defer session.Close()
	return session.Execute(cmd, timeout)
}
