package identity

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

type isimTransportFake struct {
	aid       string
	closed    []int
	calls     []string
	responses []string
}

func (f *isimTransportFake) ResolveLogicalChannelAID(app string, fallbackAID string) (string, string, error) {
	return "A0000000871004FFFFFFFF8903020000", "test_card_status", nil
}

func (f *isimTransportFake) OpenLogicalChannel(aid string) (int, error) {
	f.aid = aid
	return 7, nil
}

func (f *isimTransportFake) CloseLogicalChannel(channel int) error {
	f.closed = append(f.closed, channel)
	return nil
}

func (f *isimTransportFake) TransmitAPDU(channel int, hexAPDU string) (string, error) {
	f.calls = append(f.calls, hexAPDU)
	if len(f.responses) == 0 {
		return "6A82", nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func TestReadISIMIdentityReadsIMPIIMPUAndDomain(t *testing.T) {
	ft := &isimTransportFake{responses: []string{
		"9000",
		hexResponse(isimTLVString("310280233621715@private.att.net")),
		"9000",
		hexResponse(isimLengthString("one.att.net")),
		"6207820521000028029000",
		hexResponse(padRecord(isimTLVString("sip:310280233621715@one.att.net"), 40)),
		hexResponse(padRecord(isimLengthString("tel:+13105551212"), 40)),
	}}

	id, err := ReadISIMIdentity(ft)
	if err != nil {
		t.Fatalf("ReadISIMIdentity() error = %v", err)
	}
	if ft.aid != "A0000000871004FFFFFFFF8903020000" {
		t.Fatalf("opened AID = %q", ft.aid)
	}
	if !reflect.DeepEqual(ft.closed, []int{7}) {
		t.Fatalf("closed = %#v, want channel 7", ft.closed)
	}
	if id.IMPI != "310280233621715@private.att.net" || id.Domain != "one.att.net" {
		t.Fatalf("identity = %+v", id)
	}
	wantIMPU := []string{"sip:310280233621715@one.att.net", "tel:+13105551212"}
	if !reflect.DeepEqual(id.IMPU, wantIMPU) {
		t.Fatalf("IMPU = %#v, want %#v", id.IMPU, wantIMPU)
	}
	wantCalls := []string{
		"00A40004026F02", "00B0000000",
		"00A40004026F03", "00B0000000",
		"00A40004026F04", "00B2010428", "00B2020428",
	}
	if !reflect.DeepEqual(ft.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", ft.calls, wantCalls)
	}
}

func TestPrepareStartFallsBackAndMergesPartialISIMIdentity(t *testing.T) {
	ft := &isimTransportFake{responses: []string{
		"9000",
		hexResponse(isimTLVString("310280233621715@private.att.net")),
		"6A82",
		"6A82",
	}}
	id, err := ReadISIMIdentity(ft)
	if err != nil {
		t.Fatalf("ReadISIMIdentity() error = %v", err)
	}
	if id.IMPI == "" || id.Domain != "" || len(id.IMPU) != 0 {
		t.Fatalf("identity = %+v, want partial IMPI only", id)
	}

	prepared, err := PrepareStart(PrepareStartInput{
		Profile: Profile{IMSI: "310280233621715"},
		Access:  partialAccess{id: id},
	})
	if err != nil {
		t.Fatalf("PrepareStart() err = %v, want partial ISIM fallback", err)
	}
	if prepared.IMSIdentity.RequestedSource != IMSIdentitySourceISIM || prepared.IMSIdentity.ActualSource != IMSIdentitySourceISIM {
		t.Fatalf("prepared identity = %+v, want ISIM source", prepared.IMSIdentity)
	}
	if prepared.IMSIdentity.AKAAppPreference != AKAAppPreferenceISIM {
		t.Fatalf("AKAAppPreference = %q, want %q", prepared.IMSIdentity.AKAAppPreference, AKAAppPreferenceISIM)
	}
	if prepared.IMSIdentity.IMPI != "310280233621715@private.att.net" || prepared.IMSIdentity.IMPU != "sip:310280233621715@private.att.net" || prepared.IMSIdentity.Domain != "" {
		t.Fatalf("prepared identity = %+v, want profile fallback merged with partial ISIM", prepared.IMSIdentity)
	}
}

func TestPrepareStartFallsBackAndMergesDomainOnlyISIMIdentity(t *testing.T) {
	prepared, err := PrepareStart(PrepareStartInput{
		Profile: Profile{IMSI: "310280233621715"},
		Access: partialAccess{
			id: Identity{Domain: "one.att.net"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareStart() err = %v, want partial ISIM fallback", err)
	}
	if prepared.IMSIdentity.RequestedSource != IMSIdentitySourceISIM || prepared.IMSIdentity.ActualSource != IMSIdentitySourceProfile {
		t.Fatalf("prepared identity = %+v, want ISIM requested with profile actual source", prepared.IMSIdentity)
	}
	if prepared.IMSIdentity.AKAAppPreference != AKAAppPreferenceUSIM {
		t.Fatalf("AKAAppPreference = %q, want %q", prepared.IMSIdentity.AKAAppPreference, AKAAppPreferenceUSIM)
	}
	if prepared.IMSIdentity.IMPI != "310280233621715" || prepared.IMSIdentity.IMPU != "sip:310280233621715@one.att.net" || prepared.IMSIdentity.Domain != "one.att.net" {
		t.Fatalf("prepared identity = %+v, want profile fallback merged with domain-only ISIM", prepared.IMSIdentity)
	}
}

type partialAccess struct {
	id Identity
}

func (a partialAccess) GetISIMIdentity() (Identity, error) { return a.id, nil }

func TestReadISIMIdentityReturnsErrorWhenNoEFCanBeRead(t *testing.T) {
	ft := &isimTransportFake{responses: []string{"6A82", "6A82", "6A82"}}
	_, err := ReadISIMIdentity(ft)
	if err == nil {
		t.Fatal("ReadISIMIdentity() err=nil, want joined read error")
	}
	if !strings.Contains(err.Error(), "read EF_IMPI") {
		t.Fatalf("err = %v, want EF read context", err)
	}
}

func TestPrepareStartPreservesAllZeroTwoDigitMNC(t *testing.T) {
	prepared, err := PrepareStart(PrepareStartInput{
		Profile: Profile{
			IMSI: "454003063219947",
			MCC:  "454",
			MNC:  "00",
		},
	})
	if err != nil {
		t.Fatalf("PrepareStart() error = %v", err)
	}
	if prepared.EffectiveCarrier.MNC != "00" {
		t.Fatalf("effective MNC = %q, want 00", prepared.EffectiveCarrier.MNC)
	}
	wantEPDG := "epdg.epc.mnc000.mcc454.pub.3gppnetwork.org"
	if prepared.EPDGAddr != wantEPDG {
		t.Fatalf("EPDGAddr = %q, want %q", prepared.EPDGAddr, wantEPDG)
	}
}

func isimTLVString(s string) []byte {
	return append([]byte{0x80, byte(len(s))}, []byte(s)...)
}

func isimLengthString(s string) []byte {
	return append([]byte{byte(len(s))}, []byte(s)...)
}

func hexResponse(body []byte) string {
	out := append(append([]byte(nil), body...), 0x90, 0x00)
	return strings.ToUpper(hex.EncodeToString(out))
}

func padRecord(body []byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = 0xFF
	}
	copy(out, body)
	return out
}
