package identity

import (
	"errors"
	"fmt"
	"strings"

	"github.com/iniwex5/vowifi-go/runtimehost/simauth"
)

const (
	IMSIdentitySourceProfile = "profile"
	IMSIdentitySourceISIM    = "isim"

	AKAAppPreferenceUSIM       = "usim"
	AKAAppPreferenceAuto       = "auto"
	AKAAppPreferenceISIM       = "isim"
	AKAAppPreferenceISIMStrict = "isim_strict"
)

type Profile struct {
	IMSI string
	MCC  string
	MNC  string
	IMEI string
	SMSC string
}

type Identity struct {
	IMPI   string
	IMPU   []string
	Domain string
}

type IMSIdentityResolution struct {
	RequestedSource  string
	ActualSource     string
	AKAAppPreference string
	Applied          bool
	IMPI             string
	IMPU             string
	Domain           string
}

type EffectiveCarrier struct {
	MCC      string
	MNC      string
	PresetID string
}

type PreparedSession struct {
	Profile            Profile
	EffectiveCarrier   EffectiveCarrier
	EPDGAddr           string
	EPDGSource         string
	IdentityIMEISource string
	IMSIdentity        IMSIdentityResolution
}

type PrepareStartInput struct {
	DeviceID            string
	Profile             Profile
	RuntimeEPDGOverride string
	Access              interface {
		GetISIMIdentity() (Identity, error)
	}
}

func NormalizeProfile(p Profile) Profile {
	p.IMSI = strings.TrimSpace(p.IMSI)
	p.MCC = strings.TrimSpace(p.MCC)
	p.MNC = strings.TrimSpace(p.MNC)
	p.IMEI = strings.TrimSpace(p.IMEI)
	p.SMSC = strings.TrimSpace(p.SMSC)
	if p.MCC == "" && len(p.IMSI) >= 3 {
		p.MCC = p.IMSI[:3]
	}
	if p.MNC == "" && len(p.IMSI) >= 6 {
		p.MNC = p.IMSI[3:6]
	}
	return p
}

func PrepareStart(in PrepareStartInput) (PreparedSession, error) {
	profile := NormalizeProfile(in.Profile)
	if profile.IMSI == "" {
		return PreparedSession{}, errors.New("IMSI is empty")
	}
	prepared := PreparedSession{
		Profile: profile,
		EffectiveCarrier: EffectiveCarrier{
			MCC:      profile.MCC,
			MNC:      profile.MNC,
			PresetID: profile.MCC + profile.MNC,
		},
		EPDGAddr:           defaultEPDG(profile),
		EPDGSource:         "derived",
		IdentityIMEISource: "profile",
		IMSIdentity: IMSIdentityResolution{
			RequestedSource:  IMSIdentitySourceProfile,
			ActualSource:     IMSIdentitySourceProfile,
			AKAAppPreference: AKAAppPreferenceUSIM,
			Applied:          true,
			IMPI:             profile.IMSI,
			IMPU:             "sip:" + profile.IMSI,
			Domain:           "",
		},
	}
	if override := strings.TrimSpace(in.RuntimeEPDGOverride); override != "" {
		prepared.EPDGAddr = override
		prepared.EPDGSource = "redirect"
	}
	if in.Access != nil {
		id, err := in.Access.GetISIMIdentity()
		if err == nil && (strings.TrimSpace(id.IMPI) != "" || len(id.IMPU) > 0 || strings.TrimSpace(id.Domain) != "") {
			if strings.TrimSpace(id.IMPI) == "" || len(id.IMPU) == 0 || strings.TrimSpace(id.Domain) == "" {
				return PreparedSession{}, fmt.Errorf("ISIM 身份不完整: impi=%t impu=%d domain=%t",
					strings.TrimSpace(id.IMPI) != "", len(id.IMPU), strings.TrimSpace(id.Domain) != "")
			}
			prepared.IMSIdentity = IMSIdentityResolution{
				RequestedSource:  IMSIdentitySourceISIM,
				ActualSource:     IMSIdentitySourceISIM,
				AKAAppPreference: AKAAppPreferenceISIMStrict,
				Applied:          true,
				IMPI:             strings.TrimSpace(id.IMPI),
				IMPU:             strings.TrimSpace(id.IMPU[0]),
				Domain:           strings.TrimSpace(id.Domain),
			}
		}
	}
	return prepared, nil
}

func defaultEPDG(p Profile) string {
	mcc, mnc := strings.TrimSpace(p.MCC), strings.TrimSpace(p.MNC)
	if mcc == "" || mnc == "" {
		return ""
	}
	return fmt.Sprintf("epdg.epc.mnc%s.mcc%s.pub.3gppnetwork.org", leftPad(mnc, 3), mcc)
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}

func ReadISIMIdentity(access interface {
	OpenLogicalChannel(aid string) (int, error)
	CloseLogicalChannel(channel int) error
	TransmitAPDU(channel int, hexAPDU string) (string, error)
}) (Identity, error) {
	if access == nil {
		return Identity{}, errors.New("nil ISIM access")
	}
	aid, _, err := simauth.ResolveAID(access, "isim", simauth.ISIMAIDPrefix, simauth.ISIMAIDPrefix)
	if err != nil {
		return Identity{}, err
	}
	channel, err := access.OpenLogicalChannel(aid)
	if err != nil {
		return Identity{}, fmt.Errorf("open ISIM logical channel: %w", err)
	}
	defer func() { _ = access.CloseLogicalChannel(channel) }()

	var id Identity
	var readErrs []error

	if raw, _, err := simauth.ReadTransparentEF(access, channel, 0x6F02); err == nil {
		id.IMPI = decodeISIMString(raw)
	} else {
		readErrs = append(readErrs, fmt.Errorf("read EF_IMPI: %w", err))
	}

	if raw, _, err := simauth.ReadTransparentEF(access, channel, 0x6F03); err == nil {
		id.Domain = decodeISIMString(raw)
	} else {
		readErrs = append(readErrs, fmt.Errorf("read EF_DOMAIN: %w", err))
	}

	if records, _, err := simauth.ReadLinearFixedEF(access, channel, 0x6F04, 16); err == nil {
		for _, rec := range records {
			if impu := decodeISIMString(rec); impu != "" && !containsString(id.IMPU, impu) {
				id.IMPU = append(id.IMPU, impu)
			}
		}
	} else {
		readErrs = append(readErrs, fmt.Errorf("read EF_IMPU: %w", err))
	}

	if strings.TrimSpace(id.IMPI) != "" || strings.TrimSpace(id.Domain) != "" || len(id.IMPU) > 0 {
		return id, nil
	}
	return Identity{}, errors.Join(readErrs...)
}

func decodeISIMString(raw []byte) string {
	data := trimISIMPadding(raw)
	if len(data) == 0 {
		return ""
	}
	if data[0] == 0x80 {
		if v, ok := decodeISIMDataObject(data[1:]); ok {
			return decodeISIMStringValue(v)
		}
	}
	if v, ok := simauth.FindTLV(data, 0x80); ok {
		if s := decodeISIMStringValue(v); s != "" {
			return s
		}
	}
	return decodeISIMStringValue(data)
}

func decodeISIMDataObject(data []byte) ([]byte, bool) {
	if len(data) == 0 {
		return nil, false
	}
	l := int(data[0])
	data = data[1:]
	if l&0x80 != 0 {
		n := l & 0x7F
		if n == 0 || n > 3 || len(data) < n {
			return nil, false
		}
		l = 0
		for _, b := range data[:n] {
			l = (l << 8) | int(b)
		}
		data = data[n:]
	}
	if l < 0 || len(data) < l {
		return nil, false
	}
	return data[:l], true
}

func decodeISIMStringValue(data []byte) string {
	data = trimISIMPadding(data)
	if len(data) == 0 {
		return ""
	}
	if l := int(data[0]); l > 0 && len(data) >= 1+l {
		return strings.TrimSpace(string(trimISIMPadding(data[1 : 1+l])))
	}
	return strings.TrimSpace(string(data))
}

func trimISIMPadding(data []byte) []byte {
	start := 0
	for start < len(data) && (data[start] == 0x00 || data[start] == 0xFF) {
		start++
	}
	end := len(data)
	for end > start && (data[end-1] == 0x00 || data[end-1] == 0xFF) {
		end--
	}
	return data[start:end]
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
