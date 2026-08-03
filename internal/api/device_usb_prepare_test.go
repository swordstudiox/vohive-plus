package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/iniwex5/vohive/internal/device"
)

func TestHandleDevicePrepareUSBReturnsSummary(t *testing.T) {
	restoreUSBPrepareStub(t)
	prepareWSLUSBForAPIFn = func(context.Context) (device.WSLUSBPrepareResult, error) {
		return device.WSLUSBPrepareResult{
			SupportedDeviceFound: true,
			Prepared:             true,
			Message:              "WSL USB ready",
			Actions:              []string{"modprobe:qmi_wwan", "bind-qmi:1-1:1.4"},
			Devices: []device.WSLUSBPreparedDevice{
				{
					USBPath:      "/sys/bus/usb/devices/1-1",
					VendorID:     "2ca3",
					ProductID:    "4006",
					ControlPath:  "/dev/cdc-wdm0",
					NetInterface: "wwan0",
					ATPorts:      []string{"/dev/ttyUSB2"},
				},
			},
		}, nil
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/devices/actions/prepare-usb", nil)

	(&Server{}).handleDevicePrepareUSB(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status   string                        `json:"status"`
		Prepared bool                          `json:"prepared"`
		Message  string                        `json:"message"`
		Devices  []device.WSLUSBPreparedDevice `json:"devices"`
		Actions  []string                      `json:"actions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v body=%s", err, rec.Body.String())
	}
	if body.Status != "ok" || !body.Prepared || body.Message != "WSL USB ready" {
		t.Fatalf("unexpected response: %+v", body)
	}
	if len(body.Devices) != 1 || body.Devices[0].ControlPath != "/dev/cdc-wdm0" {
		t.Fatalf("unexpected devices: %+v", body.Devices)
	}
	if len(body.Actions) != 2 {
		t.Fatalf("unexpected actions: %+v", body.Actions)
	}
}

func TestHandleDevicePrepareUSBReturnsFailure(t *testing.T) {
	restoreUSBPrepareStub(t)
	prepareWSLUSBForAPIFn = func(context.Context) (device.WSLUSBPrepareResult, error) {
		return device.WSLUSBPrepareResult{}, errors.New("permission denied")
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/devices/actions/prepare-usb", nil)

	(&Server{}).handleDevicePrepareUSB(ctx)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode: %v body=%s", err, rec.Body.String())
	}
	if body["status"] != "error" || body["message"] == "" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func restoreUSBPrepareStub(t *testing.T) {
	t.Helper()
	orig := prepareWSLUSBForAPIFn
	t.Cleanup(func() {
		prepareWSLUSBForAPIFn = orig
	})
}
