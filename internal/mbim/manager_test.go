package mbimcore

import (
	"context"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func TestManagerControlDevice(t *testing.T) {
	m := New("/dev/cdc-wdm0", "auto")
	if m.ControlDevice() != "/dev/cdc-wdm0" {
		t.Fatalf("ControlDevice = %q", m.ControlDevice())
	}
}

func TestManagerDelegatesToDevice(t *testing.T) {
	dev := newTestDevice(t)
	m := &Manager{controlDevice: "/dev/cdc-wdm0", dev: dev}
	caps, err := m.DeviceCaps(context.Background())
	if err != nil {
		t.Fatalf("DeviceCaps: %v", err)
	}
	if caps.DeviceID != "356938035643809" {
		t.Fatalf("IMEI = %q", caps.DeviceID)
	}
	_ = mbim.UUIDBasicConnect
}

func TestManagerOpenSubscribes(t *testing.T) {
	var sawSubscribe bool
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, isSubscribe := mbim.TestAnswerOpenAndSubscribe(w)
		if isSubscribe {
			sawSubscribe = true
		}
		return out, send
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	if !sawSubscribe {
		t.Fatal("Open did not send event subscription")
	}
}

func TestManagerOnNewSMS(t *testing.T) {
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, _ := mbim.TestAnswerOpenAndSubscribe(w)
		return out, send
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	fired := make(chan struct{}, 1)
	m.OnNewSMS(func() { fired <- struct{}{} })
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	m.mu.Lock()
	mon := m.mon
	m.mu.Unlock()
	if mon == nil {
		t.Fatal("monitor not initialized")
	}
	if !mbim.TestEmitIndication(ft, mbim.UUIDSMS, mbim.CIDSMSRead, nil) {
		t.Fatal("fake transport did not accept indication")
	}

	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("Manager.OnNewSMS 未触发")
	}
}

func TestManagerOnSimStatusChanged(t *testing.T) {
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, _ := mbim.TestAnswerOpenAndSubscribe(w)
		return out, send
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	fired := make(chan struct{}, 1)
	m.OnSimStatusChanged(func() { fired <- struct{}{} })
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	info := mbim.TestSubscriberReadyInfo(1, "460001234567890", "89860012345678901234")
	if !mbim.TestEmitIndication(ft, mbim.UUIDBasicConnect, mbim.CIDBasicConnectSubscriberReadyStatus, info) {
		t.Fatal("fake transport did not accept indication")
	}

	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("Manager.OnSimStatusChanged 未触发")
	}
}

func TestManagerOnSlotStatus(t *testing.T) {
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, _ := mbim.TestAnswerOpenAndSubscribe(w)
		return out, send
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	type result struct{ slot, state uint32 }
	fired := make(chan result, 1)
	m.OnSlotStatus(func(slotIndex, state uint32) { fired <- result{slotIndex, state} })
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	info := mbim.TestSlotInfoStatusInfo(2, mbim.UICCSlotStateActiveEsim)
	if !mbim.TestEmitIndication(ft, mbim.UUIDMSBasicConnectExtensions, mbim.CIDMSBasicConnectExtSlotInfoStatus, info) {
		t.Fatal("fake transport did not accept indication")
	}

	select {
	case r := <-fired:
		if r.slot != 2 || r.state != mbim.UICCSlotStateActiveEsim {
			t.Fatalf("got slot=%d state=%d, want slot=2 state=%d", r.slot, r.state, mbim.UICCSlotStateActiveEsim)
		}
	case <-time.After(time.Second):
		t.Fatal("Manager.OnSlotStatus 未触发")
	}
}

func TestManagerUICCDelegates(t *testing.T) {
	ft := mbim.NewFakeTransport(mbim.TestAnswerUICC)
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	ch, err := m.OpenChannel(context.Background(), []byte{0xA0, 0x00})
	if err != nil {
		t.Fatalf("OpenChannel: %v", err)
	}
	resp, err := m.TransmitAPDU(context.Background(), ch, []byte{0x00, 0xA4})
	if err != nil {
		t.Fatalf("TransmitAPDU: %v", err)
	}
	if len(resp) != 2 || resp[0] != 0x90 {
		t.Fatalf("resp = %x", resp)
	}
	if err := m.CloseChannel(context.Background(), ch); err != nil {
		t.Fatalf("CloseChannel: %v", err)
	}
}

func TestManagerProbeUICC(t *testing.T) {
	ft := mbim.NewFakeTransport(mbim.TestAnswerUICC)
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	if !m.ProbeUICCSupport(context.Background()) {
		t.Fatal("expected UICC support")
	}
}

func newTestDevice(t *testing.T) *mbim.Device {
	t.Helper()
	ft := mbim.NewFakeTransport(func(written []byte) ([]byte, bool) {
		return mbim.TestAnswerDeviceCaps(written, "356938035643809")
	})
	d := mbim.NewDevice(ft)
	if err := d.Open(context.Background(), 4096); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestManagerCalculateAKA(t *testing.T) {
	ft := mbim.NewFakeTransport(mbim.TestAnswerAuthAKA)
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	res, ik, ck, _, err := m.CalculateAKA(context.Background(), make([]byte, 16), make([]byte, 16))
	if err != nil {
		t.Fatalf("CalculateAKA: %v", err)
	}
	if len(res) == 0 || len(ik) != 16 || len(ck) != 16 {
		t.Fatalf("res=%x ik=%x ck=%x", res, ik, ck)
	}
}
