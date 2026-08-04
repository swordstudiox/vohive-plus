package mbimcore

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

func TestManagerExecuteUSSDWaitsForIndication(t *testing.T) {
	var ft mbim.Transport
	ft = mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, isUSSD := mbim.TestAnswerOpenSubscribeAndUSSDPending(w)
		if isUSSD {
			go func() {
				_, payload := mbim.EncodeUSSDRequest("balance 10")
				info := mbim.TestUSSDResponseInfo(mbim.USSDRespNoActionRequired, 1, 0x0F, payload)
				mbim.TestEmitIndication(ft, mbim.UUIDUSSD, mbim.CIDUSSD, info)
			}()
		}
		return out, send
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	got, err := m.ExecuteUSSD(context.Background(), "*100#", time.Second)
	if err != nil {
		t.Fatalf("ExecuteUSSD: %v", err)
	}
	if got.Response != mbim.USSDRespNoActionRequired || got.DCS != 0x0F || got.Text != "balance 10" {
		t.Fatalf("ExecuteUSSD() = %+v", got)
	}
}

func TestManagerExecuteUSSDReturnsTerminalEmptyCommandDone(t *testing.T) {
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		return mbim.TestAnswerOpenSubscribeAndUSSDTerminal(w, mbim.USSDRespTerminated)
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	start := time.Now()
	got, err := m.ExecuteUSSD(context.Background(), "*100#", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("ExecuteUSSD: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 100*time.Millisecond {
		t.Fatalf("ExecuteUSSD took %s, want immediate terminal response", elapsed)
	}
	if got.Response != mbim.USSDRespTerminated || got.Text != "" || got.RawHex != "" {
		t.Fatalf("ExecuteUSSD() = %+v, want terminated empty result", got)
	}
}

func TestManagerContinueUSSDSendsContinueAction(t *testing.T) {
	var seenAction uint32
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
			if svc.Equal(mbim.UUIDUSSD) && cid == mbim.CIDUSSD {
				seenAction = binary.LittleEndian.Uint32(w[48:])
				_, payload := mbim.EncodeUSSDRequest("continued")
				info := mbim.TestUSSDResponseInfo(mbim.USSDRespNoActionRequired, 1, 0x0F, payload)
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

	got, err := m.ContinueUSSD(context.Background(), "1", time.Second)
	if err != nil {
		t.Fatalf("ContinueUSSD: %v", err)
	}
	if seenAction != mbim.USSDActionContinue {
		t.Fatalf("USSD action=%d want continue action %d", seenAction, mbim.USSDActionContinue)
	}
	if got.Response != mbim.USSDRespNoActionRequired || got.Text != "continued" {
		t.Fatalf("ContinueUSSD() = %+v", got)
	}
}

func TestManagerCancelUSSDUnblocksPendingExecute(t *testing.T) {
	ussdSent := make(chan struct{}, 1)
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, isUSSD := mbim.TestAnswerOpenSubscribeAndUSSDPending(w)
		if isUSSD {
			select {
			case ussdSent <- struct{}{}:
			default:
			}
		}
		return out, send
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	done := make(chan error, 1)
	go func() {
		_, err := m.ExecuteUSSD(context.Background(), "*100#", time.Minute)
		done <- err
	}()

	select {
	case <-ussdSent:
	case <-time.After(time.Second):
		t.Fatal("USSD initiate was not sent")
	}
	if err := m.CancelUSSD(context.Background()); err != nil {
		t.Fatalf("CancelUSSD: %v", err)
	}

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cancel") {
			t.Fatalf("ExecuteUSSD error = %v, want cancel-related error", err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("CancelUSSD did not unblock ExecuteUSSD")
	}
}

func TestManagerCloseUnblocksPendingExecuteUSSD(t *testing.T) {
	ussdSent := make(chan struct{}, 1)
	ft := mbim.NewFakeTransport(func(w []byte) ([]byte, bool) {
		out, send, isUSSD := mbim.TestAnswerOpenSubscribeAndUSSDPending(w)
		if isUSSD {
			select {
			case ussdSent <- struct{}{}:
			default:
			}
		}
		return out, send
	})
	m := &Manager{controlDevice: "/dev/cdc-wdm0", transportMode: "direct"}
	if err := m.openWithTransport(context.Background(), ft); err != nil {
		t.Fatalf("open: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := m.ExecuteUSSD(context.Background(), "*100#", time.Minute)
		done <- err
	}()

	select {
	case <-ussdSent:
	case <-time.After(time.Second):
		t.Fatal("USSD initiate was not sent")
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "closed") {
			t.Fatalf("ExecuteUSSD error = %v, want closed-related error", err)
		}
	case <-time.After(150 * time.Millisecond):
		t.Fatal("Close did not unblock ExecuteUSSD")
	}
}
