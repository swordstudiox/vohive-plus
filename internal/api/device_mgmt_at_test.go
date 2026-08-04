package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/backend"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/device"
	"github.com/swordstudiox/vohive-plus/internal/modem"
)

type fakeManualATSession struct {
	resp     string
	err      error
	cmd      string
	timeout  time.Duration
	closed   bool
	closeErr error
}

func (s *fakeManualATSession) Execute(cmd string, timeout time.Duration) (string, error) {
	s.cmd = cmd
	s.timeout = timeout
	return s.resp, s.err
}

func (s *fakeManualATSession) Close() error {
	s.closed = true
	return s.closeErr
}

func TestExecuteManualATOnPortUsesTransientSerialSession(t *testing.T) {
	orig := openManualATSession
	defer func() { openManualATSession = orig }()

	fake := &fakeManualATSession{resp: "OK\r\n"}
	var gotPort string
	openManualATSession = func(port string) (manualATSession, error) {
		gotPort = port
		return fake, nil
	}

	resp, err := executeManualATOnPort("/dev/ttyUSB2", "AT+CSQ", 7*time.Second)
	if err != nil {
		t.Fatalf("executeManualATOnPort() error = %v", err)
	}
	if resp != "OK\r\n" {
		t.Fatalf("executeManualATOnPort() resp = %q, want OK", resp)
	}
	if gotPort != "/dev/ttyUSB2" {
		t.Fatalf("open port = %q, want /dev/ttyUSB2", gotPort)
	}
	if fake.cmd != "AT+CSQ" || fake.timeout != 7*time.Second {
		t.Fatalf("Execute() got cmd=%q timeout=%s", fake.cmd, fake.timeout)
	}
	if !fake.closed {
		t.Fatal("manual AT session was not closed")
	}
}

func TestExecuteManualATOnPortRejectsEmptyPort(t *testing.T) {
	if _, err := executeManualATOnPort(" ", "AT", time.Second); err == nil {
		t.Fatal("executeManualATOnPort() error = nil, want empty-port error")
	}
}

func TestExecuteManualATOnPortReturnsOpenError(t *testing.T) {
	orig := openManualATSession
	defer func() { openManualATSession = orig }()

	openManualATSession = func(port string) (manualATSession, error) {
		return nil, errors.New("busy")
	}

	if _, err := executeManualATOnPort("/dev/ttyUSB2", "AT", time.Second); err == nil || err.Error() != "打开 AT 端口 /dev/ttyUSB2 失败: busy" {
		t.Fatalf("executeManualATOnPort() error = %v", err)
	}
}

func TestHandleDeviceMgmtExecuteATUsesTransientSessionForMBIMBackend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	orig := openManualATSession
	defer func() { openManualATSession = orig }()

	fake := &fakeManualATSession{resp: "OK\r\n"}
	var gotPort string
	openManualATSession = func(port string) (manualATSession, error) {
		gotPort = port
		return fake, nil
	}

	p := device.NewPool(&config.Config{})
	be := &ussdDeviceBackendStub{mode: backend.BackendMBIM}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-mbim": {
			ID:      "dev-mbim",
			Config:  config.DeviceConfig{ID: "dev-mbim", DeviceBackend: backend.BackendMBIM, ATPort: "/dev/ttyUSB9"},
			Backend: be,
		},
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-mbim"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-mbim/actions/at", strings.NewReader(`{"cmd":"AT+CSQ","timeout_ms":7000}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	server.handleDeviceMgmtExecuteAT(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if gotPort != "/dev/ttyUSB9" {
		t.Fatalf("open port = %q, want /dev/ttyUSB9", gotPort)
	}
	if fake.cmd != "AT+CSQ" || fake.timeout != 7*time.Second {
		t.Fatalf("Execute() got cmd=%q timeout=%s", fake.cmd, fake.timeout)
	}
	if !fake.closed {
		t.Fatal("manual AT session was not closed")
	}
	if !strings.Contains(rec.Body.String(), `"response":"OK\r\n"`) {
		t.Fatalf("body=%s want AT response", rec.Body.String())
	}
}

func TestManualATPortForWorkerFallsBackToModemPort(t *testing.T) {
	m, err := modem.New(config.DeviceConfig{ID: "d", DeviceBackend: "qmi", ManagePort: "/dev/ttyUSB2"})
	if err != nil {
		t.Fatalf("modem.New() error = %v", err)
	}
	// worker.Config 路径全空(零路径),只有 Modem 内存里有端口。
	w := &device.Worker{ID: "d", Config: config.DeviceConfig{ID: "d", DeviceBackend: "qmi"}, Modem: m}
	if got := manualATPortForWorker(w); got != "/dev/ttyUSB2" {
		t.Fatalf("manualATPortForWorker() = %q, want /dev/ttyUSB2", got)
	}
}
