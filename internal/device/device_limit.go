package device

import (
	"fmt"
	"strings"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

const DefaultFreeDeviceLimit = 5

func FreeDeviceLimitReached(count int) bool {
	return count >= DefaultFreeDeviceLimit
}

func FreeDeviceAddLimitMessage() string {
	return fmt.Sprintf("当前版本最多只能添加 %d 个设备", DefaultFreeDeviceLimit)
}

func FreeDeviceWorkerLimitMessage() string {
	return fmt.Sprintf("当前版本最多只能启动 %d 个设备", DefaultFreeDeviceLimit)
}

func FreeDeviceLimitAllowsConfiguredDevice(devices []config.DeviceConfig, deviceID string) bool {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return true
	}
	seen := 0
	for _, dev := range devices {
		id := strings.TrimSpace(dev.ID)
		if id == "" {
			continue
		}
		seen++
		if id == deviceID {
			return seen <= DefaultFreeDeviceLimit
		}
	}
	return true
}
