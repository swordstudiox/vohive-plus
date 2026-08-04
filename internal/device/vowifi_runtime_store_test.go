package device

import (
	"strings"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/swordstudiox/vohive-plus/internal/vowifihost"
)

func (p *Pool) voWiFiRuntimeStore() vowifihost.RuntimeStore {
	if p == nil {
		return vowifihost.NewRuntimeStore()
	}
	return p.voWiFiHost().RuntimeStore()
}

func (p *Pool) currentVoWiFiRuntimeEpoch(deviceID string) uint64 {
	if p == nil {
		return 0
	}
	return p.voWiFiHost().CurrentEpoch(deviceID)
}

func (p *Pool) invalidateVoWiFiRuntime(deviceID, reason string) uint64 {
	if p == nil {
		return 0
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return 0
	}
	return p.voWiFiHost().InvalidateRuntime(deviceID, reason)
}

func (p *Pool) claimStartedVoWiFiApp(deviceID string, runtime any, startupEpoch uint64) bool {
	if p == nil || runtime == nil {
		return false
	}
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}

	switch v := runtime.(type) {
	case *runtimehost.Instance:
		return p.voWiFiHost().ClaimStarted(deviceID, startupEpoch, v)
	default:
		return false
	}
}
