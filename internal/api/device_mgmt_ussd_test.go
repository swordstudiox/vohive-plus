package api

import (
	"context"
	"encoding/json"
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
)

type ussdDeviceBackendStub struct {
	mode      string
	imei      string
	rebootErr error
	rebooted  bool
}

var _ backend.DeviceBackend = (*ussdDeviceBackendStub)(nil)

func (s *ussdDeviceBackendStub) GetIMEI(ctx context.Context) (string, error)     { return s.imei, nil }
func (s *ussdDeviceBackendStub) GetIMSI(ctx context.Context) (string, error)     { return "", nil }
func (s *ussdDeviceBackendStub) GetICCID(ctx context.Context) (string, error)    { return "", nil }
func (s *ussdDeviceBackendStub) GetMSISDN(ctx context.Context) (string, error)   { return "", nil }
func (s *ussdDeviceBackendStub) GetRevision(ctx context.Context) (string, error) { return "", nil }
func (s *ussdDeviceBackendStub) GetSignalInfo(ctx context.Context) (*backend.SignalInfo, error) {
	return nil, nil
}
func (s *ussdDeviceBackendStub) GetServingSystem(ctx context.Context) (*backend.ServingSystem, error) {
	return nil, nil
}
func (s *ussdDeviceBackendStub) IsSimInserted(ctx context.Context) (bool, error) { return true, nil }
func (s *ussdDeviceBackendStub) GetNativeMCCMNC(ctx context.Context) (string, string, error) {
	return "", "", nil
}
func (s *ussdDeviceBackendStub) GetNativeSPN(ctx context.Context) (string, error) { return "", nil }
func (s *ussdDeviceBackendStub) GetSIMMetadata(ctx context.Context) (*backend.SIMMetadata, error) {
	return nil, nil
}
func (s *ussdDeviceBackendStub) SendSMS(ctx context.Context, to, body string) error { return nil }
func (s *ussdDeviceBackendStub) ReadSMS(ctx context.Context, index int) (*backend.SMS, error) {
	return nil, nil
}
func (s *ussdDeviceBackendStub) DeleteSMS(ctx context.Context, index int) error { return nil }
func (s *ussdDeviceBackendStub) ListSMS(ctx context.Context) ([]backend.SMSSummary, error) {
	return nil, nil
}
func (s *ussdDeviceBackendStub) DeleteAllSMS(ctx context.Context) error { return nil }
func (s *ussdDeviceBackendStub) SetOperatingMode(ctx context.Context, mode backend.OperatingMode) error {
	return nil
}
func (s *ussdDeviceBackendStub) GetOperatingMode(ctx context.Context) (backend.OperatingMode, error) {
	return backend.ModeOnline, nil
}
func (s *ussdDeviceBackendStub) Reboot(ctx context.Context) error {
	s.rebooted = true
	return s.rebootErr
}
func (s *ussdDeviceBackendStub) OpenLogicalChannel(ctx context.Context, aid string) (int, error) {
	return 0, nil
}
func (s *ussdDeviceBackendStub) CloseLogicalChannel(ctx context.Context, channelID int) error {
	return nil
}
func (s *ussdDeviceBackendStub) TransmitAPDU(ctx context.Context, channelID int, command string) (string, error) {
	return "", nil
}
func (s *ussdDeviceBackendStub) TransmitBasicAPDU(ctx context.Context, command string) (string, error) {
	return "", nil
}
func (s *ussdDeviceBackendStub) Mode() string {
	if s.mode == "" {
		return backend.BackendAT
	}
	return s.mode
}
func (s *ussdDeviceBackendStub) Close() error { return nil }

type ussdProviderBackendStub struct {
	ussdDeviceBackendStub
	command         string
	timeout         time.Duration
	continueInput   string
	continueTimeout time.Duration
	cancelCalled    bool
}

var _ backend.USSDProvider = (*ussdProviderBackendStub)(nil)

func (s *ussdProviderBackendStub) ExecuteUSSD(ctx context.Context, command string, timeout time.Duration) (*backend.USSDResult, error) {
	s.command = command
	s.timeout = timeout
	return &backend.USSDResult{Status: 0, Text: "ok", RawText: "ok", DCS: 15}, nil
}

func (s *ussdProviderBackendStub) ContinueUSSD(ctx context.Context, input string, timeout time.Duration) (*backend.USSDResult, error) {
	s.continueInput = input
	s.continueTimeout = timeout
	return &backend.USSDResult{Status: 0, Text: "continued", RawText: "continued", DCS: 15}, nil
}

func (s *ussdProviderBackendStub) CancelUSSD(ctx context.Context) error {
	s.cancelCalled = true
	return nil
}

func TestHandleDeviceMgmtExecuteUSSDUsesBackendProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdProviderBackendStub{ussdDeviceBackendStub: ussdDeviceBackendStub{mode: backend.BackendQMI}}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-qmi": {ID: "dev-qmi", Backend: be},
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-qmi"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-qmi/actions/ussd", strings.NewReader(`{"command":" *100# ","timeout_ms":7000}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	server.handleDeviceMgmtExecuteUSSD(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if be.command != "*100#" || be.timeout != 7*time.Second {
		t.Fatalf("provider got command=%q timeout=%s", be.command, be.timeout)
	}
	var body struct {
		Status string             `json:"status"`
		Result backend.USSDResult `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response JSON error=%v body=%s", err, rec.Body.String())
	}
	if body.Status != "ok" || body.Result.Text != "ok" {
		t.Fatalf("response=%+v want ok result", body)
	}
}

func TestHandleDeviceMgmtExecuteUSSDDefaultsToStandardNetworkTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdProviderBackendStub{ussdDeviceBackendStub: ussdDeviceBackendStub{mode: backend.BackendQMI}}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-qmi": {ID: "dev-qmi", Backend: be},
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-qmi"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-qmi/actions/ussd", strings.NewReader(`{"command":"*100#"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	server.handleDeviceMgmtExecuteUSSD(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if be.timeout != 45*time.Second {
		t.Fatalf("provider timeout=%s want 45s default", be.timeout)
	}
}

func TestHandleDeviceMgmtExecuteUSSDRejectsUnsupportedBackend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdDeviceBackendStub{mode: backend.BackendQMI}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-qmi": {ID: "dev-qmi", Backend: be},
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-qmi"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-qmi/actions/ussd", strings.NewReader(`{"command":"*100#"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	server.handleDeviceMgmtExecuteUSSD(ctx)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "当前设备后端不支持 USSD") {
		t.Fatalf("body=%s want capability error", rec.Body.String())
	}
}

func TestHandleDeviceMgmtContinueUSSDUsesBackendProviderWhenVoWiFiInactive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdProviderBackendStub{ussdDeviceBackendStub: ussdDeviceBackendStub{mode: backend.BackendMBIM}}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-mbim": {ID: "dev-mbim", Backend: be},
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-mbim"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-mbim/actions/ussd/continue", strings.NewReader(`{"session_id":"cs","input":" 1 ","timeout_ms":8000}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	server.handleDeviceMgmtContinueUSSD(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if be.continueInput != "1" || be.continueTimeout != 8*time.Second {
		t.Fatalf("provider got input=%q timeout=%s", be.continueInput, be.continueTimeout)
	}
	if !strings.Contains(rec.Body.String(), `"channel":"cs"`) {
		t.Fatalf("body=%s want CS channel", rec.Body.String())
	}
}

func TestHandleDeviceMgmtCancelUSSDUsesBackendProviderWhenVoWiFiInactive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdProviderBackendStub{ussdDeviceBackendStub: ussdDeviceBackendStub{mode: backend.BackendMBIM}}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-mbim": {ID: "dev-mbim", Backend: be},
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-mbim"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-mbim/actions/ussd/cancel", strings.NewReader(`{"session_id":"cs"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	server.handleDeviceMgmtCancelUSSD(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !be.cancelCalled {
		t.Fatal("provider CancelUSSD was not called")
	}
}

// TestShouldUseATFirstRebootSkipsForQMIBackend 测试 QMI 模式设备重启不应优先走 AT+CFUN，
// 应直接使用 QMI ModeReset（backend.Reboot），仅 AT 模式设备才保留 AT 优先路径。
func TestShouldUseATFirstRebootSkipsForQMIBackend(t *testing.T) {
	if shouldUseATFirstReboot(backend.BackendQMI) {
		t.Fatal("shouldUseATFirstReboot(qmi) = true, want false — QMI 模式应直接走 QMI ModeReset")
	}
	if !shouldUseATFirstReboot(backend.BackendAT) {
		t.Fatal("shouldUseATFirstReboot(at) = false, want true — AT 模式应保留 AT 优先路径")
	}
}

func TestHandleDeviceMgmtRebootDoesNotScheduleRecoveryOnQMITransportFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdDeviceBackendStub{
		mode:      backend.BackendQMI,
		rebootErr: errors.New("write failed: write unix @->@qmi-proxy: write: broken pipe"),
	}
	worker := &device.Worker{ID: "dev-qmi", Backend: be}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-qmi": worker,
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-qmi"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-qmi/actions/reboot", nil)

	server.handleDeviceMgmtReboot(ctx)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "已转入控制面恢复") {
		t.Fatalf("body=%s should not report recovery", rec.Body.String())
	}
	snapshot := worker.HealthSnapshot()
	if snapshot.State == device.HealthStateReprobing {
		t.Fatalf("health state=%s should not enter recovery", snapshot.State)
	}
}

func TestHandleDeviceMgmtRebootDoesNotScheduleRecoveryWhenQMIControlNotReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdDeviceBackendStub{
		mode:      backend.BackendQMI,
		rebootErr: errors.New("QMI 服务未就绪: DMS"),
	}
	worker := &device.Worker{ID: "dev-qmi", Backend: be}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-qmi": worker,
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-qmi"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-qmi/actions/reboot", nil)

	server.handleDeviceMgmtReboot(ctx)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "已转入控制面恢复") {
		t.Fatalf("body=%s should not report recovery", rec.Body.String())
	}
	snapshot := worker.HealthSnapshot()
	if snapshot.State == device.HealthStateReprobing {
		t.Fatalf("health state=%s should not enter recovery", snapshot.State)
	}
	lifecycle := p.LifecycleSnapshot("dev-qmi")
	if lifecycle.Phase == device.LifecyclePhaseRebooting {
		t.Fatalf("lifecycle phase=%s should not remain rebooting after failed reboot command", lifecycle.Phase)
	}
}

func TestHandleDeviceMgmtRebootDoesNotEnterRebootingWhenCommandFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdDeviceBackendStub{
		mode:      backend.BackendAT,
		rebootErr: errors.New("AT channel unavailable"),
	}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-at": {ID: "dev-at", Backend: be},
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-at"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-at/actions/reboot", nil)

	server.handleDeviceMgmtReboot(ctx)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	lifecycle := p.LifecycleSnapshot("dev-at")
	if lifecycle.Phase == device.LifecyclePhaseRebooting {
		t.Fatalf("lifecycle phase=%s should not be set before reboot command succeeds", lifecycle.Phase)
	}
}

func TestHandleDeviceMgmtRebootRejectsMismatchedQMIIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := device.NewPool(&config.Config{})
	be := &ussdDeviceBackendStub{
		mode: backend.BackendQMI,
		imei: "222222222222222",
	}
	worker := &device.Worker{
		ID: "dev-qmi",
		Config: config.DeviceConfig{
			ID:            "dev-qmi",
			ModemIMEI:     "111111111111111",
			DeviceBackend: backend.BackendQMI,
		},
		Backend: be,
	}
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-qmi": worker,
	})
	server := &Server{pool: p}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-qmi"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-qmi/actions/reboot", nil)

	server.handleDeviceMgmtReboot(ctx)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if be.rebooted {
		t.Fatal("backend Reboot was called despite IMEI mismatch")
	}
	if !strings.Contains(rec.Body.String(), "设备路径已漂移") {
		t.Fatalf("body=%s want identity drift message", rec.Body.String())
	}
}

func TestHandleDeviceMgmtReconnectVoWiFiReturnsRestartError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := writeDeviceMgmtLimitConfig(t, `
server:
  port: ":7575"
devices:
  - id: dev-missing-worker
    interface: wwan0
    vowifi_enabled: true
`)
	if err := config.InitGlobalManager(path); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	server := &Server{pool: device.NewPool(&config.Config{}), configPath: path}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-missing-worker"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-missing-worker/vowifi/actions/reconnect", nil)

	server.handleDeviceMgmtReconnectVoWiFi(ctx)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "VoWiFi 重连失败") {
		t.Fatalf("body=%s want reconnect failure", rec.Body.String())
	}
}
