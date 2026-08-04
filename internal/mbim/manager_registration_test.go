package mbimcore

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func TestManagerSetPacketServiceSendsAttach(t *testing.T) {
	var capturedAction uint32
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h, err := mbim.DecodeHeaderForTest(w)
		if err != nil {
			return nil, false
		}
		switch h.Type {
		case mbim.MessageTypeOpen:
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		case mbim.MessageTypeCommand:
			svc := mbim.UUID{}
			copy(svc[:], w[20:36])
			cid := binary.LittleEndian.Uint32(w[36:])
			if svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectPacketService {
				capturedAction = binary.LittleEndian.Uint32(w[48:])
				info := make([]byte, 28)
				binary.LittleEndian.PutUint32(info[4:], 2)
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, info), true
			}
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		}
		return nil, false
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	got, err := m.SetPacketService(context.Background(), mbim.PacketServiceAttach)
	if err != nil {
		t.Fatalf("SetPacketService: %v", err)
	}
	if capturedAction != uint32(mbim.PacketServiceAttach) {
		t.Fatalf("captured action=%d want attach", capturedAction)
	}
	if got.State != 2 {
		t.Fatalf("packet state=%d want attached state 2", got.State)
	}
}
