package qmicore

import (
	"context"
	"testing"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

func TestManagerAPDUSessionRegistryClearsSession(t *testing.T) {
	m := &Manager{
		cfg:          config.DeviceConfig{ID: "dev-qmi"},
		apduSessions: make(map[byte]apduSessionInfo),
	}
	m.bindAPDUSession(2, "test")

	if !m.hasAPDUSession(2) {
		t.Fatal("hasAPDUSession()=false want true")
	}
	if _, ok := m.takeAPDUSession(2); !ok {
		t.Fatal("takeAPDUSession() ok=false want true")
	}
	if m.hasAPDUSession(2) {
		t.Fatal("session remained in registry")
	}
}

func TestManagerAPDUSessionRegistryPreservesClass(t *testing.T) {
	m := &Manager{
		cfg:          config.DeviceConfig{ID: "dev-qmi"},
		apduSessions: make(map[byte]apduSessionInfo),
	}
	m.bindAPDUSession(2, "vowifi_aka", apduarbiter.APDUClassUSIMAKA)

	session, ok := m.getAPDUSession(2)
	if !ok {
		t.Fatal("getAPDUSession() ok=false want true")
	}
	if session.Owner != "vowifi_aka" || session.Class != apduarbiter.APDUClassUSIMAKA {
		t.Fatalf("session = %+v, want vowifi_aka/USIMAKA", session)
	}
}

func TestManagerAPDUTransportProfileInheritsSIMAuthClass(t *testing.T) {
	m := &Manager{
		cfg:          config.DeviceConfig{ID: "dev-qmi"},
		apduSessions: make(map[byte]apduSessionInfo),
	}
	m.bindAPDUSession(2, "vowifi_aka", apduarbiter.APDUClassUSIMAKA)

	owner, class := m.apduTransportProfile(2)
	if owner != "vowifi_aka" || class != apduarbiter.APDUClassUSIMAKA {
		t.Fatalf("apduTransportProfile() = %s/%s, want vowifi_aka/USIMAKA", owner, class)
	}
}

func TestManagerAcquireAPDUTransportLeaseAllowsConcurrentQMIChannels(t *testing.T) {
	arb := apduarbiter.New("dev-qmi", apduarbiter.Options{MaxQMITransports: 3})
	m := &Manager{
		cfg:          config.DeviceConfig{ID: "dev-qmi"},
		apduArbiter:  arb,
		apduSessions: make(map[byte]apduSessionInfo),
	}

	first, err := m.acquireAPDUTransportLease(
		context.Background(),
		time.Second,
		"profile-a",
		apduarbiter.APDUClassEUICCWrite,
		2,
		apduarbiter.TransportScopeQMIChannel,
	)
	if err != nil {
		t.Fatalf("first acquire error=%v", err)
	}
	defer first.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	second, err := m.acquireAPDUTransportLease(
		ctx,
		time.Second,
		"profile-b",
		apduarbiter.APDUClassEUICCWrite,
		3,
		apduarbiter.TransportScopeQMIChannel,
	)
	if err != nil {
		t.Fatalf("second acquire different channel error=%v", err)
	}
	defer second.Release()
}
