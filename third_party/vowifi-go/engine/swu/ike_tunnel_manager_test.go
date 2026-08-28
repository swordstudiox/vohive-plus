package swu

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"

	"github.com/iniwex5/vowifi-go/engine/sim"
	"github.com/iniwex5/vowifi-go/engine/swu/eapaka"
	"github.com/iniwex5/vowifi-go/engine/swu/ikev2"
)

func TestIKEPacketTunnelManagerEstablishesPacketSession(t *testing.T) {
	ikeTransport := ikeTunnelNoopTransport{}
	espTransport := &captureESPPacketTransport{}
	var gotInit ikev2.InitConfig
	var gotAuth ikev2.FullAuthConfig
	var gotIKETransport IKETransportConfig
	var gotESPTransport ESPTransportConfig

	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{
		SIM:    ikeTunnelAKAProvider{},
		Random: bytes.NewReader(append([]byte{0xca, 0xfe, 0xba, 0xbe}, bytes.Repeat([]byte{0x55}, 64)...)),
		EPDGCandidateResolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{host}, nil
		},
		IKETransportFactory: func(cfg TunnelConfig, transport IKETransportConfig) (ikev2.InitTransport, error) {
			gotIKETransport = transport
			return ikeTransport, nil
		},
		ESPTransportFactory: func(cfg TunnelConfig, transport ESPTransportConfig) (ESPPacketTransport, error) {
			gotESPTransport = transport
			return espTransport, nil
		},
		InitRunner: func(ctx context.Context, cfg ikev2.InitConfig) (ikev2.InitResult, error) {
			gotInit = cfg
			return ikev2.InitResult{MOBIKESupported: true}, nil
		},
		AuthRunner: func(ctx context.Context, cfg ikev2.FullAuthConfig) (ikev2.FullAuthResult, error) {
			gotAuth = cfg
			child := packetChildSA(true)
			child.LocalSPI = append([]byte(nil), cfg.ChildSPI...)
			child.Configuration = &ikev2.Configuration{
				Type: ikev2.CFGReply,
				Attributes: []ikev2.ConfigurationAttribute{
					{Type: ikev2.ConfigInternalIPv4Address, Value: []byte{10, 0, 0, 2}},
					{Type: ikev2.ConfigInternalIPv4DNS, Value: []byte{10, 0, 0, 1}},
					{Type: ikev2.ConfigInternalIPv6DNS, Value: net.ParseIP("2001:db8::53").To16()},
				},
			}
			return ikev2.FullAuthResult{ChildSA: &child, NextMessageID: 3}, nil
		},
	})

	session, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID:     "dev-1",
		Mode:         DataplaneModeUserspace,
		EPDGAddress:  "epdg.example",
		OuterLocalIP: "192.0.2.10",
		IMSI:         "310280233641503",
		MCC:          "310",
		MNC:          "280",
		Identity:     IMSIdentity{IMPI: "310280233641503@private.att.net"},
	})
	if err != nil {
		t.Fatalf("EstablishTunnel() error = %v", err)
	}
	result := session.Result()
	if !result.IsReady() || result.EPDGAddress != "epdg.example" || result.LocalInnerIP != "10.0.0.2" {
		t.Fatalf("result=%+v", result)
	}
	if len(result.DNSServers) != 2 || result.DNSServers[0] != "10.0.0.1" || result.DNSServers[1] != "2001:db8::53" {
		t.Fatalf("result DNS=%+v", result.DNSServers)
	}
	result.DNSServers[0] = "198.51.100.53"
	if got := session.Result().DNSServers[0]; got != "10.0.0.1" {
		t.Fatalf("Result() leaked DNS slice, got %q", got)
	}
	if !result.MOBIKESupported || result.ChildSAIdentifier != "cafebabe/22222222" {
		t.Fatalf("result MOBIKE/child id = %+v", result)
	}
	if gotIKETransport.RemoteAddr != "epdg.example:4500" || gotIKETransport.LocalAddr != "192.0.2.10:0" || !gotIKETransport.UseNonESPMarker {
		t.Fatalf("IKE transport=%+v", gotIKETransport)
	}
	if gotESPTransport.RemoteAddr != "epdg.example:4500" || gotESPTransport.LocalAddr != "192.0.2.10:0" {
		t.Fatalf("ESP transport=%+v", gotESPTransport)
	}
	if gotInit.Transport != ikeTransport || gotInit.RemotePort != 4500 {
		t.Fatalf("init config=%+v", gotInit)
	}
	if gotAuth.Transport != ikeTransport || gotAuth.SIM == nil {
		t.Fatalf("auth config transport/SIM not wired: %+v", gotAuth)
	}
	if gotAuth.EAPIdentity != "310280233641503@private.att.net" {
		t.Fatalf("EAP identity=%q", gotAuth.EAPIdentity)
	}
	if gotAuth.InitiatorID.Type != ikev2.IDRFC822Addr || string(gotAuth.InitiatorID.Data) != gotAuth.EAPIdentity {
		t.Fatalf("initiator id=%+v", gotAuth.InitiatorID)
	}
	if !bytes.Equal(gotAuth.ChildSPI, []byte{0xca, 0xfe, 0xba, 0xbe}) {
		t.Fatalf("child SPI=%x", gotAuth.ChildSPI)
	}

	packetSession, ok := session.(PacketTunnelSession)
	if !ok {
		t.Fatalf("session type %T does not implement PacketTunnelSession", session)
	}
	if err := packetSession.SendInnerPacket(context.Background(), []byte{0x45, 0x00, 0x00, 0x14}); err != nil {
		t.Fatalf("SendInnerPacket() error = %v", err)
	}
	if len(espTransport.packets) != 1 {
		t.Fatalf("ESP packets=%d, want 1", len(espTransport.packets))
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !espTransport.closed {
		t.Fatalf("ESP transport was not closed")
	}
}

func TestIKEPacketTunnelManagerDerivesEPDGAndAKAIdentity(t *testing.T) {
	var gotAuth ikev2.FullAuthConfig
	var gotIKETransport IKETransportConfig
	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{
		SIM:      ikeTunnelAKAProvider{},
		ChildSPI: []byte{0x11, 0x22, 0x33, 0x44},
		EPDGCandidateResolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{host}, nil
		},
		IKETransportFactory: func(cfg TunnelConfig, transport IKETransportConfig) (ikev2.InitTransport, error) {
			gotIKETransport = transport
			return ikeTunnelNoopTransport{}, nil
		},
		ESPTransport: &captureESPPacketTransport{},
		InitRunner: func(ctx context.Context, cfg ikev2.InitConfig) (ikev2.InitResult, error) {
			return ikev2.InitResult{}, nil
		},
		AuthRunner: func(ctx context.Context, cfg ikev2.FullAuthConfig) (ikev2.FullAuthResult, error) {
			gotAuth = cfg
			child := packetChildSA(true)
			child.LocalSPI = append([]byte(nil), cfg.ChildSPI...)
			return ikev2.FullAuthResult{ChildSA: &child, NextMessageID: 2}, nil
		},
	})

	session, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID: "dev-1",
		Mode:     DataplaneModeUserspace,
		IMSI:     "310280233641503",
		MCC:      "310",
		MNC:      "28",
	})
	if err != nil {
		t.Fatalf("EstablishTunnel() error = %v", err)
	}
	defer session.Close(context.Background())

	wantEPDG := "epdg.epc.mnc028.mcc310.pub.3gppnetwork.org"
	wantIdentity := "0310280233641503@nai.epc.mnc028.mcc310.3gppnetwork.org"
	if gotIKETransport.EPDGAddress != wantEPDG || gotIKETransport.RemoteAddr != wantEPDG+":4500" {
		t.Fatalf("IKE transport=%+v", gotIKETransport)
	}
	if gotAuth.EAPIdentity != wantIdentity || string(gotAuth.InitiatorID.Data) != wantIdentity {
		t.Fatalf("auth identity=%q initiator=%q", gotAuth.EAPIdentity, gotAuth.InitiatorID.Data)
	}
	if session.Result().EPDGAddress != wantEPDG {
		t.Fatalf("result EPDG=%q", session.Result().EPDGAddress)
	}
}

func TestIKEPacketTunnelManagerRetriesEPDGCandidates(t *testing.T) {
	var attempts []string
	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{
		SIM:      ikeTunnelAKAProvider{},
		ChildSPI: []byte{0x11, 0x22, 0x33, 0x44},
		EPDGCandidateResolver: func(ctx context.Context, host string) ([]string, error) {
			if host != "epdg.example" {
				t.Fatalf("resolver host=%q, want epdg.example", host)
			}
			return []string{"198.51.100.1", "198.51.100.2"}, nil
		},
		IKETransportFactory: func(cfg TunnelConfig, transport IKETransportConfig) (ikev2.InitTransport, error) {
			attempts = append(attempts, transport.EPDGAddress)
			return candidateIKETransport{remote: transport.EPDGAddress}, nil
		},
		ESPTransportFactory: func(cfg TunnelConfig, transport ESPTransportConfig) (ESPPacketTransport, error) {
			return &captureESPPacketTransport{}, nil
		},
		InitRunner: func(ctx context.Context, cfg ikev2.InitConfig) (ikev2.InitResult, error) {
			tr, ok := cfg.Transport.(candidateIKETransport)
			if !ok {
				t.Fatalf("init transport type=%T, want candidateIKETransport", cfg.Transport)
			}
			if tr.remote == "198.51.100.1" {
				return ikev2.InitResult{}, errors.New("dial timeout")
			}
			if tr.remote != "198.51.100.2" {
				t.Fatalf("init transport remote=%q, want second candidate", tr.remote)
			}
			return ikev2.InitResult{Keys: ikev2.IKEKeys{}}, nil
		},
		AuthRunner: func(ctx context.Context, cfg ikev2.FullAuthConfig) (ikev2.FullAuthResult, error) {
			if len(attempts) != 2 {
				t.Fatalf("auth attempted after %d transport attempts, want 2", len(attempts))
			}
			child := packetChildSA(true)
			child.LocalSPI = append([]byte(nil), cfg.ChildSPI...)
			return ikev2.FullAuthResult{ChildSA: &child, NextMessageID: 2}, nil
		},
	})

	session, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID:    "dev-1",
		Mode:        DataplaneModeUserspace,
		EPDGAddress: "epdg.example",
		IMSI:        "310280233641503",
		MCC:         "310",
		MNC:         "280",
	})
	if err != nil {
		t.Fatalf("EstablishTunnel() error = %v", err)
	}
	defer session.Close(context.Background())

	if got := attempts; len(got) != 2 || got[0] != "198.51.100.1" || got[1] != "198.51.100.2" {
		t.Fatalf("attempts=%+v", got)
	}
	if got := session.Result().EPDGAddress; got != "epdg.example" {
		t.Fatalf("result EPDG=%q, want epdg.example", got)
	}
}

func TestIKEPacketTunnelManagerPreservesExplicitEPDGPortForCandidates(t *testing.T) {
	var gotIKETransport IKETransportConfig
	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{
		SIM:      ikeTunnelAKAProvider{},
		ChildSPI: []byte{0x11, 0x22, 0x33, 0x44},
		EPDGCandidateResolver: func(ctx context.Context, host string) ([]string, error) {
			if host != "epdg.example" {
				t.Fatalf("resolver host=%q, want epdg.example", host)
			}
			return []string{"198.51.100.7"}, nil
		},
		IKETransportFactory: func(cfg TunnelConfig, transport IKETransportConfig) (ikev2.InitTransport, error) {
			gotIKETransport = transport
			return ikeTunnelNoopTransport{}, nil
		},
		ESPTransport: &captureESPPacketTransport{},
		InitRunner: func(ctx context.Context, cfg ikev2.InitConfig) (ikev2.InitResult, error) {
			if cfg.RemotePort != 4501 {
				t.Fatalf("init remote port=%d, want 4501", cfg.RemotePort)
			}
			return ikev2.InitResult{}, nil
		},
		AuthRunner: func(ctx context.Context, cfg ikev2.FullAuthConfig) (ikev2.FullAuthResult, error) {
			child := packetChildSA(true)
			child.LocalSPI = append([]byte(nil), cfg.ChildSPI...)
			return ikev2.FullAuthResult{ChildSA: &child, NextMessageID: 2}, nil
		},
	})

	session, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID:    "dev-1",
		Mode:        DataplaneModeUserspace,
		EPDGAddress: "epdg.example:4501",
		IMSI:        "310280233641503",
		MCC:         "310",
		MNC:         "280",
	})
	if err != nil {
		t.Fatalf("EstablishTunnel() error = %v", err)
	}
	defer session.Close(context.Background())

	if gotIKETransport.EPDGAddress != "198.51.100.7:4501" || gotIKETransport.RemoteAddr != "198.51.100.7:4501" {
		t.Fatalf("IKE transport=%+v, want candidate to preserve explicit ePDG port", gotIKETransport)
	}
	if got := session.Result().EPDGAddress; got != "epdg.example:4501" {
		t.Fatalf("result EPDG=%q, want original ePDG with port", got)
	}
}

func TestIKEPacketTunnelManagerCarriesReauthenticationState(t *testing.T) {
	initialKeys := eapaka.Keys{
		MK:    bytes.Repeat([]byte{0x01}, 20),
		KEncr: bytes.Repeat([]byte{0x02}, eapaka.KeyLengthKEncr),
		KAut:  bytes.Repeat([]byte{0x03}, eapaka.KeyLengthKAut),
		MSK:   bytes.Repeat([]byte{0x04}, eapaka.KeyLengthMSK),
		EMSK:  bytes.Repeat([]byte{0x05}, eapaka.KeyLengthEMSK),
	}
	nextKeys := initialKeys
	nextKeys.MSK = bytes.Repeat([]byte{0x06}, eapaka.KeyLengthMSK)
	nextKeys.EMSK = bytes.Repeat([]byte{0x07}, eapaka.KeyLengthEMSK)
	var gotAuth ikev2.FullAuthConfig
	var gotState EAPReauthenticationState
	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{
		SIM:          ikeTunnelAKAProvider{},
		ChildSPI:     []byte{0x11, 0x22, 0x33, 0x44},
		Transport:    ikeTunnelNoopTransport{},
		ESPTransport: &captureESPPacketTransport{},
		EPDGCandidateResolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{host}, nil
		},
		Reauthentication: EAPReauthenticationState{
			Identity:  "reauth-2",
			Counter:   2,
			CounterOK: true,
			Keys:      initialKeys,
		},
		OnReauthenticationState: func(state EAPReauthenticationState) {
			gotState = state
		},
		InitRunner: func(ctx context.Context, cfg ikev2.InitConfig) (ikev2.InitResult, error) {
			return ikev2.InitResult{}, nil
		},
		AuthRunner: func(ctx context.Context, cfg ikev2.FullAuthConfig) (ikev2.FullAuthResult, error) {
			gotAuth = cfg
			child := packetChildSA(true)
			child.LocalSPI = append([]byte(nil), cfg.ChildSPI...)
			return ikev2.FullAuthResult{
				ChildSA:            &child,
				EAPKeys:            nextKeys,
				EAPNextReauthID:    "reauth-3",
				EAPNextPseudonym:   "pseudo-3",
				EAPReauthenticated: true,
				EAPReauthCounter:   3,
				NextMessageID:      2,
			}, nil
		},
	})

	session, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID:    "dev-1",
		Mode:        DataplaneModeUserspace,
		EPDGAddress: "epdg.example",
		IMSI:        "310280233641503",
		MCC:         "310",
		MNC:         "280",
	})
	if err != nil {
		t.Fatalf("EstablishTunnel() error = %v", err)
	}
	defer session.Close(context.Background())

	if gotAuth.EAPReauthIdentity != "reauth-2" || gotAuth.EAPReauthCounter != 2 || !gotAuth.EAPReauthCounterOK {
		t.Fatalf("auth reauth config identity=%q counter=%d ok=%t", gotAuth.EAPReauthIdentity, gotAuth.EAPReauthCounter, gotAuth.EAPReauthCounterOK)
	}
	if !bytes.Equal(gotAuth.EAPKeys.MSK, initialKeys.MSK) {
		t.Fatalf("auth EAP keys were not carried")
	}
	if gotState.Identity != "reauth-3" || gotState.NextPseudonym != "pseudo-3" || gotState.Counter != 3 || !gotState.CounterOK || !gotState.Reauthenticated {
		t.Fatalf("callback state=%+v", gotState)
	}
	if !bytes.Equal(gotState.Keys.MSK, nextKeys.MSK) || !bytes.Equal(manager.Config.Reauthentication.Keys.EMSK, nextKeys.EMSK) {
		t.Fatalf("updated keys callback=%+v manager=%+v", gotState.Keys, manager.Config.Reauthentication.Keys)
	}
	gotState.Keys.MSK[0] = 0xff
	if manager.Config.Reauthentication.Keys.MSK[0] == 0xff {
		t.Fatal("callback state leaked key slice into manager state")
	}
}

func TestIKEPacketTunnelManagerIgnoresIncompleteReauthenticationState(t *testing.T) {
	initialKeys := eapaka.Keys{
		MK:    bytes.Repeat([]byte{0x01}, 20),
		KEncr: bytes.Repeat([]byte{0x02}, eapaka.KeyLengthKEncr),
		KAut:  bytes.Repeat([]byte{0x03}, eapaka.KeyLengthKAut),
		MSK:   bytes.Repeat([]byte{0x04}, eapaka.KeyLengthMSK),
		EMSK:  bytes.Repeat([]byte{0x05}, eapaka.KeyLengthEMSK),
	}
	var gotAuth ikev2.FullAuthConfig
	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{
		SIM:          ikeTunnelAKAProvider{},
		ChildSPI:     []byte{0x11, 0x22, 0x33, 0x44},
		Transport:    ikeTunnelNoopTransport{},
		ESPTransport: &captureESPPacketTransport{},
		EPDGCandidateResolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{host}, nil
		},
		Reauthentication: EAPReauthenticationState{
			Counter:   9,
			CounterOK: true,
			Keys:      initialKeys,
		},
		InitRunner: func(ctx context.Context, cfg ikev2.InitConfig) (ikev2.InitResult, error) {
			return ikev2.InitResult{}, nil
		},
		AuthRunner: func(ctx context.Context, cfg ikev2.FullAuthConfig) (ikev2.FullAuthResult, error) {
			gotAuth = cfg
			child := packetChildSA(true)
			child.LocalSPI = append([]byte(nil), cfg.ChildSPI...)
			return ikev2.FullAuthResult{
				ChildSA:         &child,
				EAPKeys:         initialKeys,
				EAPNextReauthID: "reauth-1",
				NextMessageID:   2,
			}, nil
		},
	})

	session, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID:    "dev-1",
		Mode:        DataplaneModeUserspace,
		EPDGAddress: "epdg.example",
		IMSI:        "310280233641503",
		MCC:         "310",
		MNC:         "280",
	})
	if err != nil {
		t.Fatalf("EstablishTunnel() error = %v", err)
	}
	defer session.Close(context.Background())

	if gotAuth.EAPReauthIdentity != "" || gotAuth.EAPReauthCounter != 0 || gotAuth.EAPReauthCounterOK {
		t.Fatalf("auth reauth config identity=%q counter=%d ok=%t", gotAuth.EAPReauthIdentity, gotAuth.EAPReauthCounter, gotAuth.EAPReauthCounterOK)
	}
	if len(gotAuth.EAPKeys.KAut) != 0 || len(gotAuth.EAPKeys.KEncr) != 0 {
		t.Fatalf("incomplete EAP keys were carried: %+v", gotAuth.EAPKeys)
	}
	if manager.Config.Reauthentication.Identity != "reauth-1" || manager.Config.Reauthentication.Counter != 0 || !manager.Config.Reauthentication.CounterOK {
		t.Fatalf("updated state=%+v", manager.Config.Reauthentication)
	}
}

func TestIKEPacketTunnelManagerResetsReauthenticationCounterAfterFullAuth(t *testing.T) {
	previousKeys := eapaka.Keys{
		MK:    bytes.Repeat([]byte{0x01}, 20),
		KEncr: bytes.Repeat([]byte{0x02}, eapaka.KeyLengthKEncr),
		KAut:  bytes.Repeat([]byte{0x03}, eapaka.KeyLengthKAut),
		MSK:   bytes.Repeat([]byte{0x04}, eapaka.KeyLengthMSK),
		EMSK:  bytes.Repeat([]byte{0x05}, eapaka.KeyLengthEMSK),
	}
	nextKeys := eapaka.Keys{
		MK:    bytes.Repeat([]byte{0x11}, 20),
		KEncr: bytes.Repeat([]byte{0x12}, eapaka.KeyLengthKEncr),
		KAut:  bytes.Repeat([]byte{0x13}, eapaka.KeyLengthKAut),
		MSK:   bytes.Repeat([]byte{0x14}, eapaka.KeyLengthMSK),
		EMSK:  bytes.Repeat([]byte{0x15}, eapaka.KeyLengthEMSK),
	}
	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{
		SIM:          ikeTunnelAKAProvider{},
		ChildSPI:     []byte{0x11, 0x22, 0x33, 0x44},
		Transport:    ikeTunnelNoopTransport{},
		ESPTransport: &captureESPPacketTransport{},
		EPDGCandidateResolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{host}, nil
		},
		Reauthentication: EAPReauthenticationState{
			Identity:            "old-reauth",
			Counter:             9,
			CounterOK:           true,
			Keys:                previousKeys,
			LastAcceptedCounter: 9,
			LastRejectedCounter: 4,
		},
		InitRunner: func(ctx context.Context, cfg ikev2.InitConfig) (ikev2.InitResult, error) {
			return ikev2.InitResult{}, nil
		},
		AuthRunner: func(ctx context.Context, cfg ikev2.FullAuthConfig) (ikev2.FullAuthResult, error) {
			if cfg.EAPReauthIdentity != "old-reauth" || cfg.EAPReauthCounter != 9 || !cfg.EAPReauthCounterOK {
				t.Fatalf("auth reauth config identity=%q counter=%d ok=%t", cfg.EAPReauthIdentity, cfg.EAPReauthCounter, cfg.EAPReauthCounterOK)
			}
			child := packetChildSA(true)
			child.LocalSPI = append([]byte(nil), cfg.ChildSPI...)
			return ikev2.FullAuthResult{
				ChildSA:         &child,
				EAPKeys:         nextKeys,
				EAPNextReauthID: "new-reauth",
				NextMessageID:   2,
			}, nil
		},
	})

	session, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID:    "dev-1",
		Mode:        DataplaneModeUserspace,
		EPDGAddress: "epdg.example",
		IMSI:        "310280233641503",
		MCC:         "310",
		MNC:         "280",
	})
	if err != nil {
		t.Fatalf("EstablishTunnel() error = %v", err)
	}
	defer session.Close(context.Background())

	state := manager.Config.Reauthentication
	if state.Identity != "new-reauth" || state.Counter != 0 || !state.CounterOK || state.LastAcceptedCounter != 0 || state.LastRejectedCounter != 0 {
		t.Fatalf("updated state=%+v", state)
	}
	if !bytes.Equal(state.Keys.MSK, nextKeys.MSK) {
		t.Fatalf("updated keys=%+v", state.Keys)
	}
}

func TestIKEPacketTunnelManagerRejectsMissingSIM(t *testing.T) {
	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{})
	_, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID:    "dev-1",
		Mode:        DataplaneModeUserspace,
		EPDGAddress: "epdg.example",
		IMSI:        "310280233641503",
	})
	if !errors.Is(err, ErrInvalidIKETunnelManager) {
		t.Fatalf("EstablishTunnel() err=%v, want ErrInvalidIKETunnelManager", err)
	}
}

func TestIKEPacketTunnelManagerRejectsMissingChildSA(t *testing.T) {
	manager := NewIKEPacketTunnelManager(IKEPacketTunnelManagerConfig{
		SIM:          ikeTunnelAKAProvider{},
		ChildSPI:     []byte{0x11, 0x22, 0x33, 0x44},
		Transport:    ikeTunnelNoopTransport{},
		ESPTransport: &captureESPPacketTransport{},
		EPDGCandidateResolver: func(ctx context.Context, host string) ([]string, error) {
			return []string{host}, nil
		},
		InitRunner: func(ctx context.Context, cfg ikev2.InitConfig) (ikev2.InitResult, error) {
			return ikev2.InitResult{}, nil
		},
		AuthRunner: func(ctx context.Context, cfg ikev2.FullAuthConfig) (ikev2.FullAuthResult, error) {
			return ikev2.FullAuthResult{NextMessageID: 2}, nil
		},
	})
	_, err := manager.EstablishTunnel(context.Background(), TunnelConfig{
		DeviceID:    "dev-1",
		Mode:        DataplaneModeUserspace,
		EPDGAddress: "epdg.example",
		IMSI:        "310280233641503",
	})
	if !errors.Is(err, ErrTunnelNotReady) {
		t.Fatalf("EstablishTunnel() err=%v, want ErrTunnelNotReady", err)
	}
}

type ikeTunnelNoopTransport struct{}

func (ikeTunnelNoopTransport) ExchangeIKE(context.Context, []byte) ([]byte, error) {
	return nil, errors.New("unexpected IKE exchange")
}

type candidateIKETransport struct {
	remote string
}

func (t candidateIKETransport) ExchangeIKE(context.Context, []byte) ([]byte, error) {
	return nil, errors.New("candidate transport should not be used directly in test")
}

type ikeTunnelAKAProvider struct{}

func (ikeTunnelAKAProvider) CalculateAKA(rand16, autn16 []byte) (sim.AKAResult, error) {
	return sim.AKAResult{
		RES: []byte{0x01, 0x02, 0x03, 0x04},
		CK:  bytes.Repeat([]byte{0x10}, 16),
		IK:  bytes.Repeat([]byte{0x20}, 16),
	}, nil
}
