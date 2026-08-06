package device

import (
	"context"
	"strings"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	mbimcore "github.com/swordstudiox/vohive-plus/internal/mbim"
)

type fakeController struct {
	connected   bool
	connErr     error
	rotated     int
	connectHook func()
}

func (f *fakeController) Connect() error {
	if f.connectHook != nil {
		f.connectHook()
	}
	f.connected = f.connErr == nil
	return f.connErr
}

func (f *fakeController) Disconnect() error { f.connected = false; return nil }
func (f *fakeController) IsConnected() bool { return f.connected }
func (f *fakeController) RotateIP() error {
	f.rotated++
	return nil
}
func (f *fakeController) GetPrivateIP() string                        { return "10.1.2.3" }
func (f *fakeController) GetPrivateIPv6() string                      { return "" }
func (f *fakeController) GetPublicIPv4AndV6NoCache() (string, string) { return "203.0.113.1", "" }

func TestNetControllerPrefersQMIThenMBIM(t *testing.T) {
	w := &Worker{}
	if w.NetworkController() != nil {
		t.Fatal("empty worker should have nil controller")
	}
}

func TestWorkerStartNetworkUsesController(t *testing.T) {
	fc := &fakeController{}
	w := &Worker{
		netOverride: fc,
		Config:      config.DeviceConfig{NetworkEnabled: true, RoamingEnabled: true},
	}
	if err := w.StartNetwork(); err != nil {
		t.Fatalf("StartNetwork: %v", err)
	}
	if !fc.connected {
		t.Fatal("controller.Connect was not called")
	}
	if err := w.StopNetwork(); err != nil {
		t.Fatalf("StopNetwork: %v", err)
	}
	if fc.connected {
		t.Fatal("controller.Disconnect was not called")
	}
}

func TestWorkerStartNetworkBlocksDataWhenRoamingDisallowed(t *testing.T) {
	fc := &fakeController{}
	w := &Worker{
		netOverride: fc,
		Config:      config.DeviceConfig{NetworkEnabled: true, RoamingEnabled: false},
	}
	w.state.Runtime.RegStatus = 5

	err := w.StartNetwork()
	if err == nil {
		t.Fatal("StartNetwork() error=nil, want data roaming disabled error")
	}
	if !strings.Contains(err.Error(), "data_roaming_disabled") {
		t.Fatalf("StartNetwork() error=%v want data_roaming_disabled", err)
	}
	if fc.connected {
		t.Fatal("controller.Connect should not be called while roaming data is disabled")
	}
}

func TestWorkerStartNetworkBlocksDataWhenRegistrationRefreshFindsRoaming(t *testing.T) {
	events := []string{}
	ctrl := &startNetworkMBIMBackendStub{
		events:  &events,
		serving: &backend.ServingSystem{RegStatus: 5, RegStatusText: "registered-roaming", PSAttached: true},
	}
	fc := &fakeController{connectHook: func() {
		events = append(events, "connect")
	}}
	w := &Worker{
		ID:          "dev-mbim-roaming",
		Config:      config.DeviceConfig{ID: "dev-mbim-roaming", DeviceBackend: backend.BackendMBIM, NetworkEnabled: true, RoamingEnabled: false},
		Backend:     ctrl,
		MBIMCore:    &mbimcore.Manager{},
		netOverride: fc,
	}
	w.state.Runtime.RegStatus = 1

	err := w.StartNetwork()
	if err == nil {
		t.Fatal("StartNetwork() error=nil, want data roaming disabled after registration refresh")
	}
	if !strings.Contains(err.Error(), "data_roaming_disabled") {
		t.Fatalf("StartNetwork() error=%v want data_roaming_disabled", err)
	}
	if fc.connected {
		t.Fatal("controller.Connect should not be called after refreshed roaming status")
	}
	if w.state.Runtime.RegStatus != 5 {
		t.Fatalf("runtime reg status=%d want refreshed roaming status 5", w.state.Runtime.RegStatus)
	}
}

func TestWorkerRuntimeServingSystemStopsConnectedDataWhenRoamingDisallowed(t *testing.T) {
	fc := &fakeController{connected: true}
	w := &Worker{
		ID:          "dev-roaming-transition",
		Config:      config.DeviceConfig{ID: "dev-roaming-transition", NetworkEnabled: true, RoamingEnabled: false},
		netOverride: fc,
	}
	w.state.Runtime.RegStatus = 1

	w.updateRuntimeServingSystem(&backend.ServingSystem{RegStatus: 5, RegStatusText: "registered-roaming", PSAttached: true})

	if fc.connected {
		t.Fatal("connected data network must be stopped when registration changes to roaming and data roaming is disabled")
	}
}

type startNetworkMBIMBackendStub struct {
	events  *[]string
	serving *backend.ServingSystem
}

func (s *startNetworkMBIMBackendStub) GetIMEI(context.Context) (string, error)     { return "", nil }
func (s *startNetworkMBIMBackendStub) GetIMSI(context.Context) (string, error)     { return "", nil }
func (s *startNetworkMBIMBackendStub) GetICCID(context.Context) (string, error)    { return "", nil }
func (s *startNetworkMBIMBackendStub) GetMSISDN(context.Context) (string, error)   { return "", nil }
func (s *startNetworkMBIMBackendStub) GetRevision(context.Context) (string, error) { return "", nil }
func (s *startNetworkMBIMBackendStub) GetSignalInfo(context.Context) (*backend.SignalInfo, error) {
	return &backend.SignalInfo{}, nil
}
func (s *startNetworkMBIMBackendStub) GetServingSystem(context.Context) (*backend.ServingSystem, error) {
	if s.serving == nil {
		return &backend.ServingSystem{RegStatus: 1, PSAttached: true}, nil
	}
	return s.serving, nil
}
func (s *startNetworkMBIMBackendStub) IsSimInserted(context.Context) (bool, error) { return true, nil }
func (s *startNetworkMBIMBackendStub) GetNativeMCCMNC(context.Context) (string, string, error) {
	return "", "", nil
}
func (s *startNetworkMBIMBackendStub) GetNativeSPN(context.Context) (string, error) { return "", nil }
func (s *startNetworkMBIMBackendStub) GetSIMMetadata(context.Context) (*backend.SIMMetadata, error) {
	return nil, nil
}
func (s *startNetworkMBIMBackendStub) SendSMS(context.Context, string, string) error { return nil }
func (s *startNetworkMBIMBackendStub) ReadSMS(context.Context, int) (*backend.SMS, error) {
	return nil, nil
}
func (s *startNetworkMBIMBackendStub) DeleteSMS(context.Context, int) error { return nil }
func (s *startNetworkMBIMBackendStub) ListSMS(context.Context) ([]backend.SMSSummary, error) {
	return nil, nil
}
func (s *startNetworkMBIMBackendStub) DeleteAllSMS(context.Context) error { return nil }
func (s *startNetworkMBIMBackendStub) SetOperatingMode(context.Context, backend.OperatingMode) error {
	return nil
}
func (s *startNetworkMBIMBackendStub) GetOperatingMode(context.Context) (backend.OperatingMode, error) {
	return backend.ModeOnline, nil
}
func (s *startNetworkMBIMBackendStub) Reboot(context.Context) error { return nil }
func (s *startNetworkMBIMBackendStub) OpenLogicalChannel(context.Context, string) (int, error) {
	return 0, nil
}
func (s *startNetworkMBIMBackendStub) CloseLogicalChannel(context.Context, int) error {
	return nil
}
func (s *startNetworkMBIMBackendStub) TransmitAPDU(context.Context, int, string) (string, error) {
	return "", nil
}
func (s *startNetworkMBIMBackendStub) TransmitBasicAPDU(context.Context, string) (string, error) {
	return "", nil
}
func (s *startNetworkMBIMBackendStub) Mode() string { return backend.BackendMBIM }
func (s *startNetworkMBIMBackendStub) Close() error { return nil }
func (s *startNetworkMBIMBackendStub) SetOperatorSelection(_ context.Context, req backend.SetOperatorSelectionRequest) (backend.OperatorSelection, error) {
	return backend.OperatorSelection{Mode: req.Mode, PLMN: req.PLMN}, nil
}
func (s *startNetworkMBIMBackendStub) AttachPacketService(context.Context) error {
	*s.events = append(*s.events, "attach")
	if s.serving != nil {
		s.serving.PSAttached = true
	}
	return nil
}

func TestWorkerStartNetworkEnsuresMBIMRegistrationBeforeConnect(t *testing.T) {
	events := []string{}
	ctrl := &startNetworkMBIMBackendStub{
		events:  &events,
		serving: &backend.ServingSystem{RegStatus: 1, PSAttached: false},
	}
	w := &Worker{
		ID:       "dev-mbim",
		Config:   config.DeviceConfig{ID: "dev-mbim", DeviceBackend: backend.BackendMBIM, NetworkEnabled: true},
		Backend:  ctrl,
		MBIMCore: &mbimcore.Manager{},
		netOverride: &fakeController{connectHook: func() {
			events = append(events, "connect")
		}},
	}

	if err := w.StartNetwork(); err != nil {
		t.Fatalf("StartNetwork: %v", err)
	}
	if strings.Join(events, ",") != "attach,connect" {
		t.Fatalf("events=%v want attach before connect", events)
	}
}
