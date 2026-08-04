package device

import (
	"context"
	"testing"
	"time"

	mbimcore "github.com/swordstudiox/vohive-plus/internal/mbim"
	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func TestBindMBIMStateIndicationsTriggersHandleSIMStatusEvent(t *testing.T) {
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, _ := mbim.TestAnswerOpenAndSubscribe(w)
		return out, send
	})
	mc, err := mbimcore.NewForTest(ft)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}
	defer mc.Close()

	p := &Pool{ctx: context.Background()}
	w := &Worker{ID: "dev1", MBIMCore: mc}
	p.bindMBIMStateIndications(w)

	info := mbim.TestSubscriberReadyInfo(1, "460001234567890", "89860012345678901234")
	if !mbim.TestEmitIndication(ft, mbim.UUIDBasicConnect, mbim.CIDBasicConnectSubscriberReadyStatus, info) {
		t.Fatal("fake transport did not accept indication")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.simEventMu.Lock()
		_, scheduled := p.simEventTimers["dev1"]
		p.simEventMu.Unlock()
		if scheduled {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("bindMBIMStateIndications did not trigger handleSIMStatusEvent")
}

func TestBindMBIMStateIndicationsNilWorkerOrCoreIsNoop(t *testing.T) {
	p := &Pool{ctx: context.Background()}
	p.bindMBIMStateIndications(nil)
	p.bindMBIMStateIndications(&Worker{ID: "dev1"})
}

func TestBindMBIMSlotIndicationsWakesVoWiFiWithoutTouchingEsimMgr(t *testing.T) {
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, _ := mbim.TestAnswerOpenAndSubscribe(w)
		return out, send
	})
	mc, err := mbimcore.NewForTest(ft)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}
	defer mc.Close()

	p := &Pool{ctx: context.Background()}
	w := &Worker{ID: "dev1", MBIMCore: mc}
	p.bindMBIMSlotIndications(w)

	info := mbim.TestSlotInfoStatusInfo(0, mbim.UICCSlotStateActiveEsim)
	if !mbim.TestEmitIndication(ft, mbim.UUIDMSBasicConnectExtensions, mbim.CIDMSBasicConnectExtSlotInfoStatus, info) {
		t.Fatal("fake transport did not accept indication")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.deviceEventWakeMu.Lock()
		_, woken := p.deviceEventWakeups["dev1"]
		p.deviceEventWakeMu.Unlock()
		if woken {
			if w.EsimMgr != nil {
				t.Fatal("bindMBIMSlotIndications must not touch worker.EsimMgr")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("bindMBIMSlotIndications did not trigger wakeDesiredVoWiFiRecoverFromDeviceEvent")
}

func TestBindMBIMSlotIndicationsNilWorkerOrCoreIsNoop(t *testing.T) {
	p := &Pool{ctx: context.Background()}
	p.bindMBIMSlotIndications(nil)
	p.bindMBIMSlotIndications(&Worker{ID: "dev1"})
}

func TestBindMBIMHealthIndicationsRecordsHealthyAndSuspect(t *testing.T) {
	ft := mbim.NewFakeTransport(mbim.TestAnswerRegisterStateWithFailures(2))
	mc, err := mbimcore.NewForTest(ft)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}
	defer mc.Close()

	p := &Pool{ctx: context.Background()}
	w := &Worker{ID: "dev1", MBIMCore: mc}
	p.bindMBIMHealthIndications(w)

	deadline := time.Now().Add(3 * time.Second)
	var sawSuspect bool
	for time.Now().Before(deadline) {
		snap := w.HealthSnapshot()
		if snap.Layer == HealthLayerMBIM && snap.State == HealthStateSuspect {
			sawSuspect = true
		}
		if sawSuspect && snap.Layer == HealthLayerMBIM && snap.State == HealthStateHealthy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("did not observe Suspect followed by Healthy MBIM watchdog events (sawSuspect=%v, final snapshot=%+v)", sawSuspect, w.HealthSnapshot())
}

func TestBindMBIMHealthIndicationsNilWorkerOrCoreIsNoop(t *testing.T) {
	p := &Pool{ctx: context.Background()}
	p.bindMBIMHealthIndications(nil)
	p.bindMBIMHealthIndications(&Worker{ID: "dev1"})
}
