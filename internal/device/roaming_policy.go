package device

import (
	"fmt"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/modem"
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

func (p *Pool) applyRoamingPreference(w *Worker, enabled bool, reason string) error {
	if w == nil {
		return nil
	}
	w.Config.RoamingEnabled = enabled
	if strings.TrimSpace(w.ResolvedATPort()) == "" && (w.Modem == nil || !w.Modem.HasATPort()) {
		return nil
	}
	_, err := ExecuteRoamingATForWorker(w, enabled, 5*time.Second)
	return err
}
