package sim

import (
	"context"
	"errors"
	"reflect"
	"testing"

	swusim "github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/pkg/mbim"
)

type factoryWorkerStub struct {
	mode     string
	caps     *mbim.Capabilities
	mbim     mbimAKAProvider
	modem    ATModem
	modemErr error
}

func (w factoryWorkerStub) BackendMode() string {
	return w.mode
}

func (w factoryWorkerStub) MBIMAKAProvider() (mbimAKAProvider, bool) {
	if w.mbim == nil {
		return nil, false
	}
	return w.mbim, true
}

func (w factoryWorkerStub) MBIMCapability() (*mbim.Capabilities, bool) {
	if w.caps == nil {
		return nil, false
	}
	return w.caps, true
}

func (w factoryWorkerStub) RuntimeModem() (ATModem, error) {
	if w.modemErr != nil {
		return nil, w.modemErr
	}
	return w.modem, nil
}

type factoryMBIMBackendStub struct {
	res  []byte
	ik   []byte
	ck   []byte
	auts []byte
	err  error
}

func (b factoryMBIMBackendStub) CalculateAKA(ctx context.Context, rand16, autn16 []byte) (res, ik, ck, auts []byte, err error) {
	return b.res, b.ik, b.ck, b.auts, b.err
}

func TestBuildAKAProviderUsesMBIMAKAWhenBackendModeIsMBIM(t *testing.T) {
	provider := BuildAKAProvider(factoryWorkerStub{
		mode: backend.BackendMBIM,
		caps: &mbim.Capabilities{Services: mbim.DeviceServices{
			Elements: []mbim.DeviceServiceElement{{Service: mbim.UUIDAuth, CIDs: []uint32{1}}},
		}},
		mbim: factoryMBIMBackendStub{
			res:  []byte{0x01, 0x02},
			ck:   []byte{0x03, 0x04},
			ik:   []byte{0x05, 0x06},
			auts: []byte{0x07},
		},
	})
	if provider == nil {
		t.Fatal("BuildAKAProvider() = nil, want MBIM provider")
	}

	got, err := provider.CalculateAKA(bytes16(0x10), bytes16(0x20))
	if err != nil {
		t.Fatalf("CalculateAKA() error = %v", err)
	}
	want := swusim.AKAResult{
		RES:  []byte{0x01, 0x02},
		CK:   []byte{0x03, 0x04},
		IK:   []byte{0x05, 0x06},
		AUTS: []byte{0x07},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CalculateAKA() = %+v, want %+v", got, want)
	}
}

func TestBuildAKAProviderMBIMFallsToChannel(t *testing.T) {
	modem := &akaProviderModemFake{
		resolvedAIDByApp: map[string]string{
			"usim": "A0000000871002FF44FF128900000100",
		},
		resolveSource: "factory_test",
		logicalResponses: []string{
			"DB02112210000102030405060708090A0B0C0D0E0F1000101112131415161718191A1B1C1D1E1F9000",
		},
	}
	provider := BuildAKAProvider(factoryWorkerStub{
		mode:  backend.BackendMBIM,
		caps:  &mbim.Capabilities{UICCChannelOK: true},
		modem: modem,
	})
	if provider == nil {
		t.Fatal("AUTH 不可用但通道可用时应返回 APDU provider")
	}
}

func TestBuildAKAProviderUsesRuntimeModemAKAWhenAvailable(t *testing.T) {
	modem := &akaProviderModemFake{
		resolvedAIDByApp: map[string]string{
			"usim": "A0000000871002FF44FF128900000100",
			"isim": "A0000000871004FF44FF128900000100",
		},
		resolveSource: "factory_test",
		logicalResponses: []string{
			"DB02112210000102030405060708090A0B0C0D0E0F1000101112131415161718191A1B1C1D1E1F9000",
			"DB02112210000102030405060708090A0B0C0D0E0F1000101112131415161718191A1B1C1D1E1F9000",
		},
	}

	provider := BuildAKAProvider(factoryWorkerStub{
		mode:  backend.BackendQMI,
		modem: modem,
	})
	if provider == nil {
		t.Fatal("BuildAKAProvider() = nil, want APDU provider")
	}

	if _, err := provider.CalculateAKA(bytes16(0x10), bytes16(0x20)); err != nil {
		t.Fatalf("CalculateAKA() error = %v", err)
	}
	isimProvider, ok := provider.(swusim.ISIMAKAProvider)
	if !ok {
		t.Fatal("BuildAKAProvider() should preserve ISIM AKA support")
	}
	if _, err := isimProvider.CalculateISIMAKA(bytes16(0x30), bytes16(0x40)); err != nil {
		t.Fatalf("CalculateISIMAKA() error = %v", err)
	}
	if !reflect.DeepEqual(modem.logicalAIDs, []string{
		"A0000000871002FF44FF128900000100",
		"A0000000871004FF44FF128900000100",
	}) {
		t.Fatalf("logical AIDs = %#v", modem.logicalAIDs)
	}
}

// MBIM Auth service 返回 AUTH_SYNC_FAILURE (status=35) 并附带 AUTS 时，
// backendAKAProvider 应将其转换为 (AKAResult{AUTS:...}, ErrSyncFailure)，
// 使 EAP-AKA 引擎能发出 AT_AUTS 重同步消息。
func TestBackendAKAProviderSyncFailureReturnsErrSyncFailureWithAUTS(t *testing.T) {
	wantAUTS := []byte{0xA0, 0xB0, 0xC0}
	stub := factoryMBIMBackendStub{
		auts: wantAUTS,
		err:  &mbim.StatusError{Op: "AUTH_AKA", Status: 35},
	}
	p := backendAKAProvider{backend: stub}
	got, err := p.CalculateAKA(bytes16(0x10), bytes16(0x20))
	if !errors.Is(err, swusim.ErrSyncFailure) {
		t.Fatalf("err = %v, want ErrSyncFailure", err)
	}
	if !reflect.DeepEqual(got.AUTS, wantAUTS) {
		t.Fatalf("AUTS = % X, want % X", got.AUTS, wantAUTS)
	}
}

// channelOrAuthAKAProvider: 逻辑通道开通道失败(SelectFailed)时应自动降级到 MBIM Auth。
func TestBuildAKAProviderChannelFallsBackToAuthOnSelectFailed(t *testing.T) {
	usimAID := "A0000000871002FF44FF128900000100"
	modem := &akaProviderModemFake{
		resolvedAIDByApp: map[string]string{"usim": usimAID},
		resolveSource:    "factory_test",
		openErrByAID: map[string]error{
			usimAID: &mbim.StatusError{Op: "UICC_OPEN_CHANNEL", Status: mbim.StatusMSSelectFailed},
		},
	}
	wantRES := []byte{0xDE, 0xAD}
	authBackend := factoryMBIMBackendStub{res: wantRES, ik: make([]byte, 16), ck: make([]byte, 16)}
	caps := &mbim.Capabilities{
		UICCChannelOK: true,
		Services: mbim.DeviceServices{
			Elements: []mbim.DeviceServiceElement{{Service: mbim.UUIDAuth, CIDs: []uint32{1}}},
		},
	}
	provider := BuildAKAProvider(factoryWorkerStub{
		mode:  backend.BackendMBIM,
		caps:  caps,
		modem: modem,
		mbim:  authBackend,
	})
	if provider == nil {
		t.Fatal("BuildAKAProvider() = nil")
	}
	got, err := provider.CalculateAKA(bytes16(0x10), bytes16(0x20))
	if err != nil {
		t.Fatalf("CalculateAKA() error = %v", err)
	}
	if !reflect.DeepEqual(got.RES, wantRES) {
		t.Fatalf("RES = % X, want % X", got.RES, wantRES)
	}
}

func TestBuildAKAProviderReturnsNilWhenNoAKAPathAvailable(t *testing.T) {
	provider := BuildAKAProvider(factoryWorkerStub{
		mode:     backend.BackendAT,
		modemErr: errors.New("modem unavailable"),
	})
	if provider != nil {
		t.Fatalf("BuildAKAProvider() = %#v, want nil", provider)
	}
}

func TestBuildAKAProviderMBIMUnsupported(t *testing.T) {
	provider := BuildAKAProvider(factoryWorkerStub{
		mode: backend.BackendMBIM,
		caps: &mbim.Capabilities{},
	})
	if provider != nil {
		t.Fatal("两条带内路都不可用时应返回 nil")
	}
}
