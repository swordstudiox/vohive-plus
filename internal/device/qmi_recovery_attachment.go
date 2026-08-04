package device

import (
	"strings"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

func (p *Pool) ResolveQMIRecoveryAttachment(cfg config.DeviceConfig) qmiRecoveryScanDecision {
	if !requiresQMICore(cfg) {
		return qmiRecoveryScanDecision{Ready: true, Reason: "non_qmi"}
	}

	live, discoveryAvailable := p.qmiRecoveryLiveCandidates(cfg)
	configuredIMEI := strings.TrimSpace(cfg.ModemIMEI)
	if configuredIMEI != "" {
		if !discoveryAvailable {
			return qmiRecoveryScanGate(cfg, live, discoveryAvailable)
		}
		for _, candidate := range live {
			if strings.TrimSpace(candidate.IMEI) == configuredIMEI {
				return qmiRecoveryScanDecision{
					Ready:      true,
					Reason:     "live_imei_match",
					Attachment: candidate.Device,
				}
			}
		}
		// IMEI 暂时不匹配（可能 DMS 还没就绪），fallback 到路径匹配
		return qmiRecoveryScanGate(cfg, live, discoveryAvailable)
	}

	return qmiRecoveryScanGate(cfg, live, discoveryAvailable)
}
