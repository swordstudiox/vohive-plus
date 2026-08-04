package device

import (
	"fmt"
	"strings"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func newWorkerBackendStrict(deviceID, backendMode, controlDevice string, m *modem.Manager, source backend.QMISource, mbimSource backend.MBIMSource) (backend.DeviceBackend, error) {
	be, err := backend.NewBackend(backendMode, controlDevice, m, source, mbimSource)
	if err != nil {
		prefix := ""
		if id := strings.TrimSpace(deviceID); id != "" {
			prefix = fmt.Sprintf("[%s] ", id)
		}
		return nil, fmt.Errorf("%s初始化 %s 后端失败: %w", prefix, backendMode, err)
	}
	return be, nil
}

func backendUsesATRuntime(mode string) bool {
	return backend.NormalizeBackendMode(mode) == backend.BackendAT
}
