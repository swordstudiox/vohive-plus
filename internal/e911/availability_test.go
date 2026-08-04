package e911

import (
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/modem"
)

func TestSetupAvailableUsesNativePLMN(t *testing.T) {
	status := modem.DeviceStatus{
		IMSI:      "999990000000001",
		NativeMCC: "310",
		NativeMNC: "280",
	}
	if !SetupAvailable(status) {
		t.Fatal("SetupAvailable=false, want true for native 310/280")
	}
}

func TestSetupAvailableRejectsUnsupportedCarrier(t *testing.T) {
	status := modem.DeviceStatus{
		IMSI:      "460001234567890",
		NativeMCC: "460",
		NativeMNC: "00",
	}
	if SetupAvailable(status) {
		t.Fatal("SetupAvailable=true, want false for unsupported carrier")
	}
}
