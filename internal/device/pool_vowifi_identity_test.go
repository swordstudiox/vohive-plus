package device

import (
	"testing"
	"time"

	"github.com/iniwex5/vowifi-go/runtimehost"
	"github.com/iniwex5/vowifi-go/runtimehost/identity"
)

type vowifiIdentityTestModem struct {
	identity identity.Identity
	err      error
}

func (m vowifiIdentityTestModem) DeviceID() string                { return "wwan0" }
func (m vowifiIdentityTestModem) IsHealthy() bool                 { return true }
func (m vowifiIdentityTestModem) IsSimInserted() bool             { return true }
func (m vowifiIdentityTestModem) QuerySIMInserted() (bool, error) { return true, nil }
func (m vowifiIdentityTestModem) GetRegStatus() (int, string)     { return 1, "已注册" }
func (m vowifiIdentityTestModem) GetNetworkMode() string          { return "LTE" }
func (m vowifiIdentityTestModem) ExecuteATSilent(string, time.Duration) (string, error) {
	return "", nil
}
func (m vowifiIdentityTestModem) OpenLogicalChannel(string) (int, error)   { return 0, nil }
func (m vowifiIdentityTestModem) CloseLogicalChannel(int) error            { return nil }
func (m vowifiIdentityTestModem) TransmitAPDU(int, string) (string, error) { return "", nil }
func (m vowifiIdentityTestModem) Stop()                                    {}
func (m vowifiIdentityTestModem) GetISIMIdentity() (identity.Identity, error) {
	if m.err != nil {
		return identity.Identity{}, m.err
	}
	return identity.Identity{
		IMPI:   m.identity.IMPI,
		IMPU:   append([]string(nil), m.identity.IMPU...),
		Domain: m.identity.Domain,
	}, nil
}

func TestVoWiFiMainStartupPrepareMergesPartialISIMIdentity(t *testing.T) {
	prepared, err := identity.PrepareStart(identity.PrepareStartInput{
		DeviceID: "wwan0",
		Profile: identity.Profile{
			IMSI: "310280233621715",
			MCC:  "310",
			MNC:  "280",
			IMEI: "350225622441987",
		},
		Access: runtimehost.NewModemAccessAdapter(vowifiIdentityTestModem{
			identity: identity.Identity{IMPI: "310280233621715@private.att.net"},
		}),
	})
	if err != nil {
		t.Fatalf("PrepareStart() err=%v, want partial ISIM fallback", err)
	}
	if prepared.IMSIdentity.RequestedSource != identity.IMSIdentitySourceISIM || prepared.IMSIdentity.ActualSource != identity.IMSIdentitySourceISIM {
		t.Fatalf("prepared identity=%+v, want ISIM source", prepared.IMSIdentity)
	}
	if prepared.IMSIdentity.AKAAppPreference != identity.AKAAppPreferenceISIM {
		t.Fatalf("AKAAppPreference=%q, want %q", prepared.IMSIdentity.AKAAppPreference, identity.AKAAppPreferenceISIM)
	}
	if prepared.IMSIdentity.IMPI != "310280233621715@private.att.net" || prepared.IMSIdentity.IMPU != "sip:310280233621715@private.att.net" || prepared.IMSIdentity.Domain != "" {
		t.Fatalf("prepared identity=%+v, want profile fallback merged with partial ISIM", prepared.IMSIdentity)
	}
}

func TestVoWiFiMainStartupUsesPreparedSessionIdentity(t *testing.T) {
	prepared, err := identity.PrepareStart(identity.PrepareStartInput{
		DeviceID: "wwan0",
		Profile: identity.Profile{
			IMSI: "310280233621715",
			MCC:  "310",
			MNC:  "280",
			IMEI: "350225622441987",
			SMSC: "+13123149810",
		},
		Access: runtimehost.NewModemAccessAdapter(vowifiIdentityTestModem{
			identity: identity.Identity{
				IMPI:   "310280233621715@private.att.net",
				IMPU:   []string{"sip:310280233621715@one.att.net"},
				Domain: "one.att.net",
			},
		}),
	})
	if err != nil {
		t.Fatalf("PrepareStart() err=%v", err)
	}
	req := runtimehost.StartRequest{
		Mode:     runtimehost.StartModeMain,
		DeviceID: "wwan0",
		Profile:  prepared.Profile,
		Prepared: &prepared,
	}
	if req.Prepared == nil {
		t.Fatal("StartRequest.Prepared=nil")
	}
	got := req.Prepared.IMSIdentity
	if got.ActualSource != identity.IMSIdentitySourceISIM || got.AKAAppPreference != identity.AKAAppPreferenceISIMStrict || !got.Applied {
		t.Fatalf("prepared identity=%+v", got)
	}
	if got.IMPI != "310280233621715@private.att.net" || got.IMPU != "sip:310280233621715@one.att.net" || got.Domain != "one.att.net" {
		t.Fatalf("identity=%+v", got)
	}
}

func TestVoWiFiMainStartupPreparedSessionAppliesRuntimeEPDGOverride(t *testing.T) {
	prepared, err := identity.PrepareStart(identity.PrepareStartInput{
		DeviceID:            "wwan0",
		RuntimeEPDGOverride: "redirect.epdg.example",
		Profile: identity.Profile{
			IMSI: "310280233621715",
			MCC:  "310",
			MNC:  "280",
			IMEI: "350225622441987",
		},
		Access: runtimehost.NewModemAccessAdapter(vowifiIdentityTestModem{
			identity: identity.Identity{
				IMPI:   "310280233621715@private.att.net",
				IMPU:   []string{"sip:310280233621715@one.att.net"},
				Domain: "one.att.net",
			},
		}),
	})
	if err != nil {
		t.Fatalf("PrepareStart() err=%v", err)
	}
	if prepared.EPDGAddr != "redirect.epdg.example" || prepared.EPDGSource != "redirect" {
		t.Fatalf("prepared EPDG=%q source=%q", prepared.EPDGAddr, prepared.EPDGSource)
	}
}

func TestVoWiFiMainStartupPrepareUsesISIMIdentity(t *testing.T) {
	prepared, err := identity.PrepareStart(identity.PrepareStartInput{
		DeviceID: "wwan0",
		Profile: identity.Profile{
			IMSI: "310280233621715",
			MCC:  "310",
			MNC:  "280",
			IMEI: "350225622441987",
		},
		Access: runtimehost.NewModemAccessAdapter(vowifiIdentityTestModem{
			identity: identity.Identity{
				IMPI:   "310280233621715@private.att.net",
				IMPU:   []string{"sip:310280233621715@one.att.net"},
				Domain: "one.att.net",
			},
		}),
	})
	if err != nil {
		t.Fatalf("PrepareStart() err=%v", err)
	}
	got := prepared.IMSIdentity
	if got.ActualSource != identity.IMSIdentitySourceISIM || got.AKAAppPreference != identity.AKAAppPreferenceISIMStrict || !got.Applied {
		t.Fatalf("resolution=%+v", got)
	}
	if got.IMPI != "310280233621715@private.att.net" || got.IMPU != "sip:310280233621715@one.att.net" || got.Domain != "one.att.net" {
		t.Fatalf("identity=%+v", got)
	}
}
