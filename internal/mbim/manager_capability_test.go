package mbimcore

import (
	"context"
	"testing"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func TestManagerBuildsCapabilityFromDeviceServices(t *testing.T) {
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		h, _ := mbim.DecodeHeaderForTest(w)
		switch h.Type {
		case mbim.MessageTypeOpen:
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		case mbim.MessageTypeCommand:
			svc := mbim.UUID{}
			copy(svc[:], w[20:36])
			cid := mbim.ReadU32ForTest(w[36:40])
			if svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList {
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
			}
		}
		return nil, false
	})
	m := New("/dev/cdc-wdm0", "direct")
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	cap := m.Capability()
	if cap == nil {
		t.Fatal("Capability 不应为 nil")
	}
	if !cap.Services.Supports(mbim.UUIDBasicConnect, mbim.CIDBasicConnectDeviceServices) {
		t.Fatal("应解析出 Basic Connect 宣告")
	}
}
