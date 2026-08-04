package mbimcore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

type fakeNetcfg struct {
	iface    string
	v4addr   string
	v4gw     string
	v4prefix int
	mtu      int
	dns      []string
	up       bool
	flushed  bool
}

func (f *fakeNetcfg) SetIPv4(iface, addr string, prefix int) error {
	f.iface, f.v4addr, f.v4prefix = iface, addr, prefix
	return nil
}

func (f *fakeNetcfg) SetIPv6(iface, addr string, prefix int) error { return nil }
func (f *fakeNetcfg) SetMTU(iface string, mtu int) error {
	f.mtu = mtu
	return nil
}
func (f *fakeNetcfg) BringUp(iface string) error { f.up = true; return nil }
func (f *fakeNetcfg) AddDefaultRoute(iface, gw string) error {
	f.v4gw = gw
	return nil
}
func (f *fakeNetcfg) SetDNS(dns []string) error { f.dns = append([]string(nil), dns...); return nil }
func (f *fakeNetcfg) Flush(iface string) error  { f.flushed = true; return nil }

func TestSetDataConfigStoresValues(t *testing.T) {
	m := New("/dev/cdc-wdm0", "auto")
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4v6"})
	if m.dataCfg.APN != "internet" || m.dataCfg.Interface != "wwan0" || m.dataCfg.IPVersion != "v4v6" {
		t.Fatalf("dataCfg not stored: %+v", m.dataCfg)
	}
}

func TestConnectActivatesAndAppliesIPv4(t *testing.T) {
	tr := mbim.NewFakeTransport(mbim.TestAnswerConnectAndIPv4Config)
	m := New("/dev/cdc-wdm0", "auto")
	fnc := &fakeNetcfg{}
	m.netcfg = fnc
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !m.IsConnected() {
		t.Fatal("IsConnected = false after Connect")
	}
	if m.GetPrivateIP() != "10.0.0.5" {
		t.Fatalf("GetPrivateIP = %q, want 10.0.0.5", m.GetPrivateIP())
	}
	if fnc.iface != "wwan0" || fnc.v4addr != "10.0.0.5" || fnc.v4prefix != 24 || fnc.v4gw != "10.0.0.1" || !fnc.up {
		t.Fatalf("netcfg not applied correctly: %+v", fnc)
	}
}

func TestConnectTimeoutDefaultMatchesLibmbimConnectTimeout(t *testing.T) {
	m := New("/dev/cdc-wdm0", "auto")
	if got := m.connectTimeoutOrDefault(); got != 120*time.Second {
		t.Fatalf("connectTimeoutOrDefault() = %v, want 120s", got)
	}
}

func TestConnectRecoversLeakedSessionOnMaxActivatedContexts(t *testing.T) {
	var activateAttempts int
	var deactivates int
	tr := mbim.NewFakeTransport(func(written []byte) ([]byte, bool) {
		h, err := mbim.DecodeHeaderForTest(written)
		if err != nil {
			return nil, false
		}
		if h.Type == mbim.MessageTypeOpen {
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		}
		if h.Type != mbim.MessageTypeCommand || len(written) < 48 {
			return nil, false
		}
		var svc mbim.UUID
		copy(svc[:], written[20:36])
		cid := mbim.ReadU32ForTest(written[36:])
		ct := mbim.ReadU32ForTest(written[40:])
		info := written[48:]
		switch {
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectRegisterState && ct == uint32(mbim.CommandTypeQuery):
			buf := make([]byte, 52)
			buf[4] = byte(registerStateHome)
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, buf), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectConnect && ct == uint32(mbim.CommandTypeSet):
			if len(info) < 8 {
				t.Fatalf("CONNECT info too short: %d", len(info))
			}
			switch mbim.ReadU32ForTest(info[4:]) {
			case mbim.ActivationCommandActivate:
				activateAttempts++
				if activateAttempts == 1 {
					return mbim.BuildCommandDoneStatusForTest(h.TransactionID, svc, cid, 0x0d, nil), true
				}
				resp := make([]byte, 36)
				resp[4] = byte(mbim.ActivationStateActivated)
				resp[12] = byte(mbim.ContextIPTypeIPv4)
				copy(resp[16:32], mbim.UUIDContextTypeInternet[:])
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, resp), true
			case mbim.ActivationCommandDeactivate:
				deactivates++
				resp := make([]byte, 36)
				resp[4] = byte(mbim.ActivationStateDeactivated)
				resp[12] = byte(mbim.ContextIPTypeDefault)
				copy(resp[16:32], mbim.UUIDContextTypeInternet[:])
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, resp), true
			default:
				t.Fatalf("unexpected CONNECT activation command: %d", mbim.ReadU32ForTest(info[4:]))
			}
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectIPConfiguration && ct == uint32(mbim.CommandTypeQuery):
			return mbim.TestAnswerConnectAndIPv4Config(written)
		}
		return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
	})

	m := New("/dev/cdc-wdm0", "auto")
	m.netcfg = &fakeNetcfg{}
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if activateAttempts != 2 {
		t.Fatalf("activateAttempts = %d, want 2", activateAttempts)
	}
	if deactivates != 1 {
		t.Fatalf("deactivates = %d, want 1", deactivates)
	}
	if !m.IsConnected() {
		t.Fatal("should be connected after stale session cleanup and retry")
	}
}

func TestConnectRetriesBusyActivate(t *testing.T) {
	var activateAttempts int
	tr := mbim.NewFakeTransport(func(written []byte) ([]byte, bool) {
		h, err := mbim.DecodeHeaderForTest(written)
		if err != nil {
			return nil, false
		}
		if h.Type == mbim.MessageTypeOpen {
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		}
		if h.Type != mbim.MessageTypeCommand || len(written) < 48 {
			return nil, false
		}
		var svc mbim.UUID
		copy(svc[:], written[20:36])
		cid := mbim.ReadU32ForTest(written[36:])
		ct := mbim.ReadU32ForTest(written[40:])
		info := written[48:]
		switch {
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectRegisterState && ct == uint32(mbim.CommandTypeQuery):
			buf := make([]byte, 52)
			buf[4] = byte(registerStateHome)
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, buf), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectConnect && ct == uint32(mbim.CommandTypeSet):
			if len(info) < 8 || mbim.ReadU32ForTest(info[4:]) != mbim.ActivationCommandActivate {
				return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
			}
			activateAttempts++
			if activateAttempts == 1 {
				return mbim.BuildCommandDoneStatusForTest(h.TransactionID, svc, cid, 0x01, nil), true
			}
			resp := make([]byte, 36)
			resp[4] = byte(mbim.ActivationStateActivated)
			resp[12] = byte(mbim.ContextIPTypeIPv4)
			copy(resp[16:32], mbim.UUIDContextTypeInternet[:])
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, resp), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectIPConfiguration && ct == uint32(mbim.CommandTypeQuery):
			return mbim.TestAnswerConnectAndIPv4Config(written)
		}
		return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
	})

	m := New("/dev/cdc-wdm0", "auto")
	m.netcfg = &fakeNetcfg{}
	m.activateRetryDelay = time.Millisecond
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if activateAttempts != 2 {
		t.Fatalf("activateAttempts = %d, want 2", activateAttempts)
	}
	if !m.IsConnected() {
		t.Fatal("should be connected after busy retry")
	}
}

func TestConnectReopensControlPlaneAfterActivateTimeout(t *testing.T) {
	first := mbim.NewFakeTransport(func(written []byte) ([]byte, bool) {
		h, err := mbim.DecodeHeaderForTest(written)
		if err != nil {
			return nil, false
		}
		if h.Type == mbim.MessageTypeOpen {
			return mbim.BuildOpenDoneForTest(h.TransactionID), true
		}
		if h.Type != mbim.MessageTypeCommand || len(written) < 48 {
			return nil, false
		}
		var svc mbim.UUID
		copy(svc[:], written[20:36])
		cid := mbim.ReadU32ForTest(written[36:])
		ct := mbim.ReadU32ForTest(written[40:])
		switch {
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectDeviceServiceSubscribeList:
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectRegisterState && ct == uint32(mbim.CommandTypeQuery):
			buf := make([]byte, 52)
			buf[4] = byte(registerStateHome)
			return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, buf), true
		case svc.Equal(mbim.UUIDBasicConnect) && cid == mbim.CIDBasicConnectConnect && ct == uint32(mbim.CommandTypeSet):
			return nil, false
		}
		return mbim.BuildCommandDoneForTest(h.TransactionID, svc, cid, nil), true
	})
	second := mbim.NewFakeTransport(mbim.TestAnswerConnectAndIPv4Config)

	var dialCalls int
	m := New("/dev/cdc-wdm0", "auto")
	m.netcfg = &fakeNetcfg{}
	m.connectTimeout = 10 * time.Millisecond
	m.dial = func(mode, path string) (mbim.Transport, error) {
		dialCalls++
		return second, nil
	}
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), first); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if dialCalls != 1 {
		t.Fatalf("dialCalls = %d, want 1", dialCalls)
	}
	if !m.IsConnected() {
		t.Fatal("should be connected after control-plane reopen")
	}
}

func TestDisconnectIdempotentWhenNotConnected(t *testing.T) {
	m := New("/dev/cdc-wdm0", "auto")
	if err := m.Disconnect(); err != nil {
		t.Fatalf("Disconnect on fresh manager should be nil, got %v", err)
	}
}

func TestRotateIPDeactivatesThenReconnects(t *testing.T) {
	tr := mbim.NewFakeTransport(mbim.TestAnswerConnectAndIPv4Config)
	m := New("/dev/cdc-wdm0", "auto")
	m.netcfg = &fakeNetcfg{}
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if err := m.RotateIP(); err != nil {
		t.Fatalf("RotateIP: %v", err)
	}
	if !m.IsConnected() {
		t.Fatal("should be reconnected after rotate")
	}
}

func TestGetPublicIPv4UsesProber(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("198.51.100.9"))
	}))
	defer srv.Close()

	m := New("/dev/cdc-wdm0", "auto")
	m.SetDataConfig(DataConfig{Interface: "", IPVersion: "v4"})
	m.publicIPURLs = []string{srv.URL}
	v4, _ := m.GetPublicIPv4AndV6NoCache()
	if v4 != "198.51.100.9" {
		t.Fatalf("v4 = %q, want 198.51.100.9", v4)
	}
}

func TestUnexpectedDeactivationTriggersReconnect(t *testing.T) {
	tr := mbim.NewFakeTransport(mbim.TestAnswerConnectAndIPv4Config)
	m := New("/dev/cdc-wdm0", "auto")
	m.netcfg = &fakeNetcfg{}
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	m.mu.Lock()
	m.connected = false
	m.mu.Unlock()

	m.handleConnectIndication(mbim.ConnectState{ActivationState: mbim.ActivationStateDeactivated})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m.IsConnected() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("did not reconnect after unexpected deactivation")
}

func TestCloseFlushesNetworkWhenConnected(t *testing.T) {
	tr := mbim.NewFakeTransport(mbim.TestAnswerConnectAndIPv4Config)
	m := New("/dev/cdc-wdm0", "auto")
	fnc := &fakeNetcfg{}
	m.netcfg = fnc
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := m.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !fnc.flushed {
		t.Fatal("Close should flush netcfg when a data session was connected")
	}
}

func TestCloseDoesNotFlushWhenNeverConnected(t *testing.T) {
	tr := mbim.NewFakeTransport(mbim.TestAnswerConnectAndIPv4Config)
	m := New("/dev/cdc-wdm0", "auto")
	fnc := &fakeNetcfg{}
	m.netcfg = fnc
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if fnc.flushed {
		t.Fatal("Close should not flush netcfg when never connected")
	}
}

func TestConcurrentConnectCallsAreSerialized(t *testing.T) {
	tr := mbim.NewFakeTransport(mbim.TestAnswerConnectAndIPv4Config)
	m := New("/dev/cdc-wdm0", "auto")
	fnc := &fakeNetcfg{}
	m.netcfg = fnc
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- m.Connect()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Connect failed: %v", err)
		}
	}
	if !m.IsConnected() {
		t.Fatal("should be connected")
	}
}

func TestConnectFailsWhenNotRegistered(t *testing.T) {
	tr := mbim.NewFakeTransport(mbim.TestAnswerRegistrationSearching)
	m := New("/dev/cdc-wdm0", "auto")
	m.netcfg = &fakeNetcfg{}
	m.registrationTimeout = 100 * time.Millisecond
	m.SetDataConfig(DataConfig{APN: "internet", Interface: "wwan0", IPVersion: "v4"})
	if err := m.openWithTransport(context.Background(), tr); err != nil {
		t.Fatalf("open: %v", err)
	}
	defer m.Close()

	start := time.Now()
	err := m.Connect()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Connect should fail when not registered")
	}
	if !errors.Is(err, ErrNetworkNotRegistered) {
		t.Fatalf("err = %v, want ErrNetworkNotRegistered", err)
	}
	if elapsed > 1*time.Second {
		t.Fatalf("Connect took %v, want < 1s with registrationTimeout=100ms", elapsed)
	}
}
