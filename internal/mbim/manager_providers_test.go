package mbimcore

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func TestManagerVisibleProvidersAndSetRegisterDelegate(t *testing.T) {
	var sawVisible, sawSet bool
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h := testMBIMHeader(w)
		switch h.typ {
		case mbim.MessageTypeOpen:
			return testOpenDone(h.tx), true
		case mbim.MessageTypeCommand:
			svc := testMBIMService(w)
			cid := testLe.Uint32(w[36:])
			ct := mbim.CommandType(testLe.Uint32(w[40:]))
			if svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList {
				return testCommandDone(h.tx, svc, cid, nil), true
			}
			switch cid {
			case mbim.CIDBasicConnectVisibleProviders:
				sawVisible = true
				if ct != mbim.CommandTypeQuery {
					t.Fatalf("visible providers command type = %d, want query", ct)
				}
				return testCommandDone(h.tx, svc, cid, []byte{0, 0, 0, 0}), true
			case mbim.CIDBasicConnectRegisterState:
				if ct != mbim.CommandTypeSet {
					t.Fatalf("register command type = %d, want set", ct)
				}
				sawSet = true
				return testCommandDone(h.tx, svc, cid, testRegisterStateInfo("310260", mbim.RegisterActionManual)), true
			}
		}
		return nil, false
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	providers, err := m.VisibleProviders(context.Background())
	if err != nil {
		t.Fatalf("VisibleProviders: %v", err)
	}
	if len(providers) != 0 || !sawVisible {
		t.Fatalf("providers=%+v sawVisible=%v", providers, sawVisible)
	}
	rs, err := m.SetRegister(context.Background(), mbim.RegisterActionManual, "310260")
	if err != nil {
		t.Fatalf("SetRegister: %v", err)
	}
	if rs.ProviderID != "310260" || !sawSet {
		t.Fatalf("register=%+v sawSet=%v", rs, sawSet)
	}
}

func TestManagerGetRegisterStateDelegates(t *testing.T) {
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h := testMBIMHeader(w)
		switch h.typ {
		case mbim.MessageTypeOpen:
			return testOpenDone(h.tx), true
		case mbim.MessageTypeCommand:
			svc := testMBIMService(w)
			cid := testLe.Uint32(w[36:])
			return testCommandDone(h.tx, svc, cid, testRegisterStateInfo("46000", mbim.RegisterActionAutomatic)), true
		}
		return nil, false
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	rs, err := m.GetRegisterState(context.Background())
	if err != nil {
		t.Fatalf("GetRegisterState: %v", err)
	}
	if rs.ProviderID != "46000" {
		t.Fatalf("ProviderID = %q, want 46000", rs.ProviderID)
	}
}

var testLe = binary.LittleEndian

type testHeader struct {
	typ mbim.MessageType
	tx  uint32
}

func testMBIMHeader(b []byte) testHeader {
	return testHeader{typ: mbim.MessageType(testLe.Uint32(b[0:])), tx: testLe.Uint32(b[8:])}
}

func testMBIMService(b []byte) mbim.UUID {
	var svc mbim.UUID
	copy(svc[:], b[20:36])
	return svc
}

func testOpenDone(tx uint32) []byte {
	b := make([]byte, 16)
	testLe.PutUint32(b[0:], uint32(mbim.MessageTypeOpenDone))
	testLe.PutUint32(b[4:], uint32(len(b)))
	testLe.PutUint32(b[8:], tx)
	return b
}

func testCommandDone(tx uint32, svc mbim.UUID, cid uint32, info []byte) []byte {
	b := make([]byte, 48+len(info))
	testLe.PutUint32(b[0:], uint32(mbim.MessageTypeCommandDone))
	testLe.PutUint32(b[4:], uint32(len(b)))
	testLe.PutUint32(b[8:], tx)
	testLe.PutUint32(b[12:], 1)
	testLe.PutUint32(b[16:], 0)
	copy(b[20:36], svc[:])
	testLe.PutUint32(b[36:], cid)
	testLe.PutUint32(b[40:], 0)
	testLe.PutUint32(b[44:], uint32(len(info)))
	copy(b[48:], info)
	return b
}

func testRegisterStateInfo(plmn string, mode uint32) []byte {
	plmnBytes := testUTF16(plmn)
	const fixed = 44
	info := make([]byte, fixed+len(plmnBytes))
	testLe.PutUint32(info[4:], 3)
	testLe.PutUint32(info[8:], mode)
	testLe.PutUint32(info[20:], fixed)
	testLe.PutUint32(info[24:], uint32(len(plmnBytes)))
	copy(info[fixed:], plmnBytes)
	return info
}

func testUTF16(s string) []byte {
	out := make([]byte, 0, len(s)*2)
	for _, r := range s {
		out = append(out, byte(r), 0)
	}
	return out
}
