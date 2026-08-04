package e911

import (
	"strings"

	"github.com/iniwex5/vowifi-go/runtimehost/carrier"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func SetupAvailable(status modem.DeviceStatus) bool {
	mcc, mnc := nativePLMN(status)
	if mcc == "" || mnc == "" {
		return false
	}
	cfg := carrier.ResolveEffectiveCarrierConfig(carrier.EffectiveCarrierConfigInput{
		MCC: mcc,
		MNC: mnc,
	})
	return cfg.E911.Enabled
}

func nativePLMN(status modem.DeviceStatus) (string, string) {
	mcc := strings.TrimSpace(status.NativeMCC)
	mnc := strings.TrimSpace(status.NativeMNC)
	if mcc != "" && mnc != "" {
		return mcc, mnc
	}
	imsi := strings.TrimSpace(status.IMSI)
	if len(imsi) >= 6 {
		return imsi[:3], imsi[3:6]
	}
	return "", ""
}
