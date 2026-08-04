package device

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

type esimIMEIBackendStub struct {
	mode string
	imei string
}

func (s *esimIMEIBackendStub) GetIMEI(ctx context.Context) (string, error)     { return s.imei, nil }
func (s *esimIMEIBackendStub) GetIMSI(ctx context.Context) (string, error)     { return "", nil }
func (s *esimIMEIBackendStub) GetICCID(ctx context.Context) (string, error)    { return "", nil }
func (s *esimIMEIBackendStub) GetMSISDN(ctx context.Context) (string, error)   { return "", nil }
func (s *esimIMEIBackendStub) GetRevision(ctx context.Context) (string, error) { return "", nil }
func (s *esimIMEIBackendStub) GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error) {
	return nil, nil
}
func (s *esimIMEIBackendStub) GetServingSystem(ctx context.Context) (*backend.ServingSystem, error) {
	return nil, nil
}
func (s *esimIMEIBackendStub) IsSimInserted(ctx context.Context) (bool, error) { return true, nil }
func (s *esimIMEIBackendStub) GetNativeMCCMNC(ctx context.Context) (string, string, error) {
	return "", "", nil
}

func (s *esimIMEIBackendStub) GetNativeSPN(ctx context.Context) (string, error) {
	return "", nil
}
func (s *esimIMEIBackendStub) GetSIMMetadata(ctx context.Context) (*backend.SIMMetadata, error) {
	return nil, nil
}
func (s *esimIMEIBackendStub) SendSMS(ctx context.Context, to, body string) error { return nil }
func (s *esimIMEIBackendStub) ReadSMS(ctx context.Context, index int) (*backend.SMS, error) {
	return nil, nil
}
func (s *esimIMEIBackendStub) DeleteSMS(ctx context.Context, index int) error { return nil }
func (s *esimIMEIBackendStub) ListSMS(ctx context.Context) ([]backend.SMSSummary, error) {
	return nil, nil
}
func (s *esimIMEIBackendStub) DeleteAllSMS(ctx context.Context) error { return nil }
func (s *esimIMEIBackendStub) SetOperatingMode(ctx context.Context, mode backend.OperatingMode) error {
	return nil
}
func (s *esimIMEIBackendStub) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	return backend.ModeOnline, nil
}
func (s *esimIMEIBackendStub) Reboot(ctx context.Context) error { return nil }
func (s *esimIMEIBackendStub) OpenLogicalChannel(ctx context.Context, aid string) (int, error) {
	return 0, nil
}
func (s *esimIMEIBackendStub) CloseLogicalChannel(ctx context.Context, channelID int) error {
	return nil
}
func (s *esimIMEIBackendStub) TransmitAPDU(ctx context.Context, channelID int, command string) (string, error) {
	return "", nil
}
func (s *esimIMEIBackendStub) Mode() string {
	if s.mode == "" {
		return backend.BackendQMI
	}
	return s.mode
}
func (s *esimIMEIBackendStub) Close() error { return nil }

func setPrivateStringField(t *testing.T, target any, fieldName, value string) {
	t.Helper()

	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		t.Fatal("target must be a non-nil pointer")
	}
	field := rv.Elem().FieldByName(fieldName)
	if !field.IsValid() {
		t.Fatalf("field %q not found", fieldName)
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetString(value)
}

func newWorkerModemWithIMEI(t *testing.T, imei string) *modem.Manager {
	t.Helper()

	m, err := modem.New(config.DeviceConfig{
		ID:     "dev-esim",
		ATPort: "/dev/ttyUSB6",
	})
	if err != nil {
		t.Fatalf("modem.New() error = %v", err)
	}
	setPrivateStringField(t, m, "imei", imei)
	return m
}

func TestNewESIMIMEIProviderPrefersBackendForQMI(t *testing.T) {
	w := &Worker{
		ID:      "dev-esim",
		Backend: &esimIMEIBackendStub{mode: backend.BackendQMI, imei: "qmi-imei"},
		Modem:   newWorkerModemWithIMEI(t, "modem-imei"),
	}

	imei, err := newESIMIMEIProvider(w)(context.Background())
	if err != nil {
		t.Fatalf("provider() error = %v", err)
	}
	if imei != "qmi-imei" {
		t.Fatalf("provider() = %q, want %q", imei, "qmi-imei")
	}
}

func TestNewESIMIMEIProviderDoesNotFallbackToATForQMI(t *testing.T) {
	w := &Worker{
		ID:      "dev-esim",
		Backend: &esimIMEIBackendStub{mode: backend.BackendQMI, imei: ""},
		Modem:   newWorkerModemWithIMEI(t, "modem-imei"),
	}

	imei, err := newESIMIMEIProvider(w)(context.Background())
	if err != nil {
		t.Fatalf("provider() error = %v", err)
	}
	if imei != "" {
		t.Fatalf("provider() = %q, want empty", imei)
	}
}

func TestNewESIMIMEIProviderFallsBackToModemForAT(t *testing.T) {
	w := &Worker{
		ID:      "dev-esim",
		Backend: &esimIMEIBackendStub{mode: backend.BackendAT, imei: ""},
		Modem:   newWorkerModemWithIMEI(t, "modem-imei"),
	}

	imei, err := newESIMIMEIProvider(w)(context.Background())
	if err != nil {
		t.Fatalf("provider() error = %v", err)
	}
	if imei != "modem-imei" {
		t.Fatalf("provider() = %q, want %q", imei, "modem-imei")
	}
}
