package device

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/esim"
)

type esimManagerQMITransportStub struct {
	controlDevice string
}

func (s *esimManagerQMITransportStub) ControlDevice() string { return s.controlDevice }
func (s *esimManagerQMITransportStub) OpenEUICCLogicalChannel(ctx context.Context, slot byte, aid []byte) (byte, error) {
	return 1, nil
}
func (s *esimManagerQMITransportStub) CloseEUICCLogicalChannel(ctx context.Context, slot byte, channel byte) error {
	return nil
}
func (s *esimManagerQMITransportStub) TransmitEUICCAPDU(ctx context.Context, slot byte, channel byte, command []byte) ([]byte, error) {
	return nil, nil
}

func TestNewESIMManagerForWorkerRequiresBackend(t *testing.T) {
	worker := &Worker{
		ID:     "dev-esim",
		Config: config.DeviceConfig{ID: "dev-esim", DeviceBackend: backend.BackendAT},
		Modem:  newWorkerModemWithIMEI(t, "modem-imei"),
	}

	_, err := newESIMManagerForWorker(worker, nil, nil, nil, nil, nil, nil)
	if err == nil || err.Error() != "AT 传输需要 device backend" {
		t.Fatalf("newESIMManagerForWorker() error = %v, want %q", err, "AT 传输需要 device backend")
	}
}

func TestNewESIMManagerForWorkerAT(t *testing.T) {
	worker := &Worker{
		ID:     "dev-esim",
		Config: config.DeviceConfig{ID: "dev-esim", DeviceBackend: backend.BackendAT},
		Modem:  newWorkerModemWithIMEI(t, "modem-imei"),
		Backend: &esimIMEIBackendStub{
			mode: backend.BackendAT,
		},
	}

	mgr, err := newESIMManagerForWorker(worker, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("newESIMManagerForWorker() error = %v", err)
	}
	if mgr == nil {
		t.Fatal("newESIMManagerForWorker() returned nil manager")
	}
}

func TestNewESIMManagerForWorkerQMI(t *testing.T) {
	worker := &Worker{
		ID:          "dev-esim",
		Config:      config.DeviceConfig{ID: "dev-esim", DeviceBackend: backend.BackendQMI},
		APDUArbiter: apduarbiter.New("dev-esim", apduarbiter.Options{}),
		Backend: &esimIMEIBackendStub{
			mode: backend.BackendQMI,
		},
	}

	mgr, err := newESIMManagerForWorker(worker, &esimManagerQMITransportStub{controlDevice: "/dev/cdc-wdm0"}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("newESIMManagerForWorker() error = %v", err)
	}
	if mgr == nil {
		t.Fatal("newESIMManagerForWorker() returned nil manager")
	}
}

type esimDiscoveryQMITransportStub struct {
	controlDevice string
	expectedAID   []byte
	openedAID     []byte
	openedAIDs    [][]byte
	eid           []byte
}

func (s *esimDiscoveryQMITransportStub) ControlDevice() string { return s.controlDevice }

func (s *esimDiscoveryQMITransportStub) OpenEUICCLogicalChannel(ctx context.Context, slot byte, aid []byte) (byte, error) {
	s.openedAID = append([]byte(nil), aid...)
	s.openedAIDs = append(s.openedAIDs, append([]byte(nil), aid...))
	if !bytes.Equal(aid, s.expectedAID) {
		return 0, fmt.Errorf("unexpected AID %X", aid)
	}
	return 1, nil
}

func (s *esimDiscoveryQMITransportStub) CloseEUICCLogicalChannel(ctx context.Context, slot byte, channel byte) error {
	return nil
}

func (s *esimDiscoveryQMITransportStub) TransmitEUICCAPDU(ctx context.Context, slot byte, channel byte, command []byte) ([]byte, error) {
	resp := []byte{0xBF, 0x3E, byte(2 + len(s.eid)), 0x5A, byte(len(s.eid))}
	resp = append(resp, s.eid...)
	resp = append(resp, 0x90, 0x00)
	return resp, nil
}

func TestNewESIMManagerForWorkerUsesStaticAIDTraversal(t *testing.T) {
	targetAID := esim.AIDs[3]
	eid, _ := hex.DecodeString("89049032000001000000113509931049")
	transport := &esimDiscoveryQMITransportStub{
		controlDevice: "/dev/cdc-wdm0",
		expectedAID:   targetAID,
		eid:           eid,
	}
	worker := &Worker{
		ID:          "dev-esim",
		Config:      config.DeviceConfig{ID: "dev-esim", DeviceBackend: backend.BackendQMI},
		APDUArbiter: apduarbiter.New("dev-esim", apduarbiter.Options{}),
		Backend: &esimIMEIBackendStub{
			mode: backend.BackendQMI,
		},
	}

	mgr, err := newESIMManagerForWorker(worker, transport, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("newESIMManagerForWorker() error=%v", err)
	}
	got, err := mgr.GetEID()
	if err != nil {
		t.Fatalf("GetEID() error=%v", err)
	}
	if got != "89049032000001000000113509931049" {
		t.Fatalf("GetEID()=%q", got)
	}
	if len(transport.openedAIDs) < 4 {
		t.Fatalf("openedAIDs=%X want static traversal through target", transport.openedAIDs)
	}
	if !bytes.Equal(transport.openedAIDs[0], esim.AIDs[0]) {
		t.Fatalf("first openedAID=%X want first static AID %X", transport.openedAIDs[0], esim.AIDs[0])
	}
	if !bytes.Equal(transport.openedAIDs[len(transport.openedAIDs)-1], targetAID) {
		t.Fatalf("last openedAID=%X want target %X", transport.openedAIDs[len(transport.openedAIDs)-1], targetAID)
	}
}
