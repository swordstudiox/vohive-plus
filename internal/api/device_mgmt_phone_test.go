package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	sgp22 "github.com/damonto/euicc-go/v2"
	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/apduarbiter"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/device"
	"github.com/swordstudiox/vohive-plus/internal/esim"
	"github.com/swordstudiox/vohive-plus/internal/modem"
	"golang.org/x/sync/singleflight"
)

func initDeviceMgmtPhoneTestDB(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "device_mgmt_phone.db")
	if err := db.Init(dbPath); err != nil {
		t.Fatalf("db.Init() error=%v", err)
	}
	t.Cleanup(func() { db.DB = nil })
}

func setNestedPrivateField(t *testing.T, target any, fieldPath []string, value any) {
	t.Helper()
	field := reflect.ValueOf(target).Elem()
	for _, name := range fieldPath {
		field = field.FieldByName(name)
		if !field.IsValid() {
			t.Fatalf("field path %v not found", fieldPath)
		}
	}
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func mgrTestSetProfilesLoader(t *testing.T, mgr *esim.Manager, loader func() ([]esim.EUICCProfiles, error)) {
	t.Helper()
	setNestedPrivateField(t, mgr, []string{"profilesLoader"}, loader)
}

func mgrTestSetOverviewCache(t *testing.T, mgr *esim.Manager, overview *esim.EsimOverview) {
	t.Helper()
	setNestedPrivateField(t, mgr, []string{"overviewCache"}, overview)
}

func newTestEsimManager() *esim.Manager {
	mgr := &esim.Manager{}
	field := reflect.ValueOf(mgr).Elem().FieldByName("sf")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(&singleflight.Group{}))
	return mgr
}

func containsAll(s string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(s, part) {
			return false
		}
	}
	return true
}

func TestBuildOverviewLiteItemIncludesLocalPhone(t *testing.T) {
	initDeviceMgmtPhoneTestDB(t)

	if err := db.UpdateSIMCardModemPhoneNumberByIMSI("imsi-overview-1", "+8613800138000"); err != nil {
		t.Fatalf("UpdateSIMCardModemPhoneNumberByIMSI() error=%v", err)
	}
	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-overview-1", "+8613900139000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}

	p := device.NewPool(&config.Config{})
	w := &device.Worker{ID: "dev-1"}
	server := &Server{pool: p}

	item := server.buildOverviewLiteItemFromWorker(
		w,
		config.DeviceConfig{ID: "dev-1", Name: "Device 1", VoWiFiEnabled: true},
		modem.DeviceStatus{IMSI: "imsi-overview-1"},
		nil,
	)

	if item.LocalPhone != "+8613900139000" {
		t.Fatalf("LocalPhone=%q want=+8613900139000", item.LocalPhone)
	}
}

func TestBuildOverviewLiteItemUsesCachedIMSIFallbackForLocalPhone(t *testing.T) {
	initDeviceMgmtPhoneTestDB(t)

	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-cached-1", "+8613911111111"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}

	p := device.NewPool(&config.Config{})
	w := &device.Worker{ID: "dev-cached-1"}
	setNestedPrivateField(t, w, []string{"state", "Identity", "IMSI"}, "imsi-cached-1")
	setNestedPrivateField(t, w, []string{"state", "Identity", "Ready"}, true)
	server := &Server{pool: p}

	item := server.buildOverviewLiteItemFromWorker(
		w,
		config.DeviceConfig{ID: "dev-cached-1", Name: "Device Cached", VoWiFiEnabled: false},
		modem.DeviceStatus{},
		nil,
	)

	if item.LocalPhone != "+8613911111111" {
		t.Fatalf("LocalPhone=%q want=+8613911111111 when cached IMSI exists", item.LocalPhone)
	}
}

func TestBuildOverviewLiteItemSuppressesCachedIMSIDuringIdentityTransition(t *testing.T) {
	initDeviceMgmtPhoneTestDB(t)

	if err := db.UpdateSIMCardVoWiFiPhoneNumberByIMSI("old-imsi", "+12133316760"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}

	p := device.NewPool(&config.Config{})
	w := &device.Worker{ID: "dev-switching-1"}
	setNestedPrivateField(t, w, []string{"state", "Identity", "IMSI"}, "old-imsi")
	setNestedPrivateField(t, w, []string{"state", "Identity", "Ready"}, false)
	setNestedPrivateField(t, w, []string{"state", "Identity", "Phase"}, "transitioning")
	server := &Server{pool: p}

	item := server.buildOverviewLiteItemFromWorker(
		w,
		config.DeviceConfig{ID: "dev-switching-1", Name: "Device Switching", VoWiFiEnabled: false},
		modem.DeviceStatus{},
		nil,
	)

	if item.LocalPhone != "" {
		t.Fatalf("LocalPhone=%q want empty while SIM identity is transitioning", item.LocalPhone)
	}
}

func TestBuildOverviewLiteItemIncludesActiveEsimProfileName(t *testing.T) {
	mgr := newTestEsimManager()
	mgrTestSetOverviewCache(t, mgr, &esim.EsimOverview{
		Profiles: []esim.EUICCProfiles{{
			EID:    "eid-a",
			AIDHex: "A000",
			Profiles: []esim.ProfileItem{
				{ICCID: "iccid-disabled", Name: "Disabled", State: 0, StateText: "未启用"},
				{ICCID: "iccid-enabled", Name: "China Mobile", State: 1, StateText: "已启用"},
			},
		}},
	})

	p := device.NewPool(&config.Config{})
	w := &device.Worker{ID: "dev-esim", EsimMgr: mgr}
	server := &Server{pool: p}

	item := server.buildOverviewLiteItemFromWorker(
		w,
		config.DeviceConfig{ID: "dev-esim", Name: "Device eSIM"},
		modem.DeviceStatus{},
		nil,
	)

	if item.ActiveESIMProfileName != "China Mobile" {
		t.Fatalf("ActiveESIMProfileName=%q want=China Mobile", item.ActiveESIMProfileName)
	}
}

func TestBuildOverviewLiteItemSeparatesControlOnlineFromDataNetwork(t *testing.T) {
	p := device.NewPool(&config.Config{})
	w := &device.Worker{ID: "dev-qmi"}
	setNestedPrivateField(t, w, []string{"state", "Runtime", "Ready"}, true)
	setNestedPrivateField(t, w, []string{"state", "Meta", "Healthy"}, true)
	server := &Server{pool: p}

	item := server.buildOverviewLiteItemFromWorker(
		w,
		config.DeviceConfig{ID: "dev-qmi", Name: "QMI Device", NetworkEnabled: false},
		modem.DeviceStatus{RegStatus: 2},
		nil,
	)

	if !item.Healthy {
		t.Fatal("Healthy=false, want true for backward-compatible control plane health")
	}
	if !item.ControlOnline {
		t.Fatal("ControlOnline=false, want true when control plane is healthy")
	}
	if item.NetworkEnabled {
		t.Fatal("NetworkEnabled=true, want false for data switch off")
	}
	if item.NetworkConnected {
		t.Fatal("NetworkConnected=true, want false when data plane is disconnected")
	}
	if item.RegistrationStateLabel != "searching" {
		t.Fatalf("RegistrationStateLabel=%q want searching", item.RegistrationStateLabel)
	}
}

func TestRegistrationStateLabel(t *testing.T) {
	tests := []struct {
		name string
		reg  int
		want string
	}{
		{name: "home", reg: 1, want: "registered"},
		{name: "roaming", reg: 5, want: "registered"},
		{name: "searching", reg: 2, want: "searching"},
		{name: "denied", reg: 3, want: "denied"},
		{name: "unknown", reg: 0, want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := registrationStateLabel(tt.reg); got != tt.want {
				t.Fatalf("registrationStateLabel(%d)=%q want %q", tt.reg, got, tt.want)
			}
		})
	}
}

func TestBuildOverviewLiteItemDoesNotTriggerEsimScanWhenActiveProfileCacheMissing(t *testing.T) {
	mgr := newTestEsimManager()
	setNestedPrivateField(t, mgr, []string{"overviewLoader"}, func() (*esim.EsimOverview, error) {
		t.Fatal("buildOverviewLiteItemFromWorker() should not trigger overview loader when cache is empty")
		return nil, nil
	})

	p := device.NewPool(&config.Config{})
	w := &device.Worker{ID: "dev-esim", EsimMgr: mgr}
	server := &Server{pool: p}

	item := server.buildOverviewLiteItemFromWorker(
		w,
		config.DeviceConfig{ID: "dev-esim", Name: "Device eSIM"},
		modem.DeviceStatus{},
		nil,
	)

	if item.ActiveESIMProfileName != "" {
		t.Fatalf("ActiveESIMProfileName=%q want empty when cache is missing", item.ActiveESIMProfileName)
	}
}

func TestHandleEsimListProfilesRefreshUsesProfilesRefreshPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := newTestEsimManager()
	mgrTestSetProfilesLoader(t, mgr, func() ([]esim.EUICCProfiles, error) {
		return []esim.EUICCProfiles{{
			EID:      "eid-a",
			AIDHex:   "A000",
			Profiles: []esim.ProfileItem{{ICCID: "iccid-a2", State: 1, StateText: "已启用"}},
		}}, nil
	})
	mgrTestSetOverviewCache(t, mgr, &esim.EsimOverview{
		ChipInfo: &esim.EUICCChipInfo{SkuName: "chip"},
		Profiles: []esim.EUICCProfiles{{
			EID:      "eid-a",
			AIDHex:   "A000",
			Profiles: []esim.ProfileItem{{ICCID: "iccid-a1", State: 1, StateText: "已启用"}},
		}},
	})

	p := device.NewPool(&config.Config{})
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-esim": {ID: "dev-esim", EsimMgr: mgr},
	})
	server := &Server{pool: p}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "dev-esim"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/devices/dev-esim/esim/profiles?refresh=true", nil)

	server.handleEsimListProfiles(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if body := recorder.Body.String(); body == "" || !containsAll(body, "iccid-a2", "A000") {
		t.Fatalf("body=%q want refreshed profiles payload", body)
	}
}

func TestHandleEsimGetOverviewRefreshUsesFullRefreshPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	mgr := newTestEsimManager()
	mgrTestSetOverviewCache(t, mgr, &esim.EsimOverview{
		ChipInfo: &esim.EUICCChipInfo{SkuName: "before"},
		Profiles: []esim.EUICCProfiles{{
			EID:      "eid-old",
			AIDHex:   "OLD",
			Profiles: []esim.ProfileItem{{ICCID: "iccid-old", State: 1, StateText: "已启用"}},
		}},
	})
	setNestedPrivateField(t, mgr, []string{"overviewLoader"}, func() (*esim.EsimOverview, error) {
		return &esim.EsimOverview{
			ChipInfo: &esim.EUICCChipInfo{SkuName: "after"},
			Profiles: []esim.EUICCProfiles{{
				EID:      "eid-new",
				AIDHex:   "NEW",
				Profiles: []esim.ProfileItem{{ICCID: "iccid-new", State: 1, StateText: "已启用"}},
			}},
		}, nil
	})

	p := device.NewPool(&config.Config{})
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-esim": {ID: "dev-esim", EsimMgr: mgr},
	})
	server := &Server{pool: p}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "id", Value: "dev-esim"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/devices/dev-esim/esim?refresh=true", nil)

	server.handleEsimGetOverview(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if body := recorder.Body.String(); body == "" || !containsAll(body, "after", "iccid-new", "NEW") {
		t.Fatalf("body=%q want fully refreshed overview payload", body)
	}
}

func TestHandleEsimGetOverviewRefreshPreservesBusyContractOnRefreshFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "operation in progress", err: esim.ErrOperationInProgress},
		{name: "apdu busy", err: apduarbiter.ErrAPDUBusy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newTestEsimManager()
			setNestedPrivateField(t, mgr, []string{"overviewLoader"}, func() (*esim.EsimOverview, error) {
				return nil, tc.err
			})

			p := device.NewPool(&config.Config{})
			setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
				"dev-esim": {ID: "dev-esim", EsimMgr: mgr},
			})
			server := &Server{pool: p}

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "id", Value: "dev-esim"}}
			ctx.Request = httptest.NewRequest(http.MethodGet, "/devices/dev-esim/esim?refresh=true", nil)

			server.handleEsimGetOverview(ctx)

			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusConflict, recorder.Body.String())
			}
			if got := recorder.Header().Get("Retry-After"); got != "2" {
				t.Fatalf("Retry-After=%q want=2", got)
			}
			if body := recorder.Body.String(); body == "" || !containsAll(body, `"busy":true`, `"code":"ESIM_BUSY"`, `"reason":"refresh_overview"`, tc.err.Error()) {
				t.Fatalf("body=%q want busy refresh-overview contract", body)
			}
		})
	}
}

func TestHandleEsimGetOverviewRefreshPreservesResponseSemanticsWhenGetFailsAfterRefresh(t *testing.T) {
	gin.SetMode(gin.TestMode)

	internalErr := errors.New("overview load failed")
	for _, tc := range []struct {
		name       string
		getErr     error
		wantStatus int
		wantParts  []string
	}{
		{
			name:       "busy after refresh",
			getErr:     esim.ErrOperationInProgress,
			wantStatus: http.StatusConflict,
			wantParts:  []string{`"busy":true`, `"code":"ESIM_BUSY"`, `"reason":"get_overview"`, esim.ErrOperationInProgress.Error()},
		},
		{
			name:       "internal after refresh",
			getErr:     internalErr,
			wantStatus: http.StatusInternalServerError,
			wantParts:  []string{internalErr.Error()},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newTestEsimManager()
			var calls int
			setNestedPrivateField(t, mgr, []string{"overviewLoader"}, func() (*esim.EsimOverview, error) {
				calls++
				if calls == 1 {
					setNestedPrivateField(t, mgr, []string{"overviewGeneration"}, uint64(2))
					return &esim.EsimOverview{ChipInfo: &esim.EUICCChipInfo{SkuName: "refreshed"}}, nil
				}
				return nil, tc.getErr
			})

			p := device.NewPool(&config.Config{})
			setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
				"dev-esim": {ID: "dev-esim", EsimMgr: mgr},
			})
			server := &Server{pool: p}

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "id", Value: "dev-esim"}}
			ctx.Request = httptest.NewRequest(http.MethodGet, "/devices/dev-esim/esim?refresh=true", nil)

			server.handleEsimGetOverview(ctx)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantStatus == http.StatusConflict {
				if got := recorder.Header().Get("Retry-After"); got != "2" {
					t.Fatalf("Retry-After=%q want=2", got)
				}
			}
			if body := recorder.Body.String(); body == "" || !containsAll(body, tc.wantParts...) {
				t.Fatalf("body=%q want parts=%v", body, tc.wantParts)
			}
		})
	}
}

func TestEsimDeleteSuccessBodyIncludesWarningFields(t *testing.T) {
	body := esimDeleteSuccessBody(esim.DeleteProfileResult{
		Warning:     "Profile 已删除，但删除通知发送未完全确认",
		WarningCode: "delete_notification_not_observed",
		SpaceDelta: &esim.SpaceDelta{
			Direction: esim.SpaceDeltaDirectionReleased,
			Bytes:     4096,
		},
	})

	if body["status"] != "ok" || body["message"] != "Profile 删除成功" {
		t.Fatalf("body=%v want ok delete success base fields", body)
	}
	if body["warning"] != "Profile 已删除，但删除通知发送未完全确认" {
		t.Fatalf("warning=%v want propagated warning", body["warning"])
	}
	if body["warning_code"] != "delete_notification_not_observed" {
		t.Fatalf("warning_code=%v want propagated warning_code", body["warning_code"])
	}
	spaceDelta, ok := body["space_delta"].(*esim.SpaceDelta)
	if !ok || spaceDelta == nil || spaceDelta.Direction != esim.SpaceDeltaDirectionReleased || spaceDelta.Bytes != 4096 {
		t.Fatalf("space_delta=%#v want released/4096", body["space_delta"])
	}
}

func TestEsimDeleteSuccessBodyOmitsSpaceDeltaWhenUnavailable(t *testing.T) {
	body := esimDeleteSuccessBody(esim.DeleteProfileResult{})
	if _, ok := body["space_delta"]; ok {
		t.Fatalf("body=%v want space_delta omitted", body)
	}
}

func TestFormatEsimDownloadDoneEventIncludesSpaceDelta(t *testing.T) {
	event := formatEsimDownloadDoneEvent(esim.DownloadProfileResult{
		SpaceDelta: &esim.SpaceDelta{
			Direction: esim.SpaceDeltaDirectionConsumed,
			Bytes:     8192,
		},
	})

	if !containsAll(event, `"step":"done"`, `"msg":"Profile 下载完成"`, `"pct":100`, `"space_delta":{"direction":"consumed","bytes":8192}`) {
		t.Fatalf("event=%q want done payload with space_delta", event)
	}
}

func TestFormatEsimDownloadDoneEventIncludesWarningFields(t *testing.T) {
	event := formatEsimDownloadDoneEvent(esim.DownloadProfileResult{
		Warning:     "Profile 下载完成，但通知未完全确认",
		WarningCode: "download_notification_handle_failed",
	})

	if !containsAll(event, `"step":"done"`, `"msg":"Profile 下载完成"`, `"pct":100`, `"warning":"Profile 下载完成，但通知未完全确认"`, `"warning_code":"download_notification_handle_failed"`) {
		t.Fatalf("event=%q want done payload with warning fields", event)
	}
}

func TestWriteEsimDownloadDoneEventWritesSingleWarningDoneFrame(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writeEsimDownloadDoneEvent(ctx, esim.DownloadProfileResult{
		Warning:     "Profile 下载完成，但通知未完全确认",
		WarningCode: "download_notification_handle_failed",
	})

	body := recorder.Body.String()
	if !containsAll(body, `data: {"step":"done"`, `"warning":"Profile 下载完成，但通知未完全确认"`, `"warning_code":"download_notification_handle_failed"`) {
		t.Fatalf("body=%q want SSE done frame with warning fields", body)
	}
}

func TestFormatEsimDownloadErrorEventKeepsLegacyFieldsAndAddsCode(t *testing.T) {
	event := formatEsimDownloadErrorEvent(esim.NewDownloadProfileError(sgp22.LoadBoundProfilePackageError{
		BPPCommandID: 5,
		ErrorReason:  10,
	}))

	if !containsAll(event,
		`"step":"error"`,
		`"msg":"下载失败: eUICC 安装 profile 时空间不足，请删除未使用的 profile 后重试"`,
		`"pct":-1`,
		`"code":"euicc_insufficient_memory"`,
		`"details":"loadProfileElements,installFailedDueToInsufficientMemoryForProfile"`,
	) {
		t.Fatalf("event=%q want legacy error fields plus code/details", event)
	}
}

func TestFormatEsimDownloadErrorEventSupportsPlainError(t *testing.T) {
	event := formatEsimDownloadErrorEvent(errors.New("network down"))

	if !containsAll(event, `"step":"error"`, `"msg":"下载失败: network down"`, `"pct":-1`) {
		t.Fatalf("event=%q want legacy plain error fields", event)
	}
	if strings.Contains(event, `"code"`) || strings.Contains(event, `"details"`) {
		t.Fatalf("event=%q should not include structured fields for plain errors", event)
	}
}

func TestWriteEsimDeleteSuccessJSONWritesWarningFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writeEsimDeleteSuccessJSON(ctx, esim.DeleteProfileResult{
		Warning:     "Profile 已删除，但删除通知发送未完全确认",
		WarningCode: "delete_notification_not_observed",
	})

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if body := recorder.Body.String(); body == "" || !containsAll(body, `"status":"ok"`, `"message":"Profile 删除成功"`, `"warning":"Profile 已删除，但删除通知发送未完全确认"`, `"warning_code":"delete_notification_not_observed"`) {
		t.Fatalf("body=%q want success-with-warning delete payload", body)
	}
}

func TestEsimDownloadExecPropagatesWarningResult(t *testing.T) {
	var gotIMEI string
	result, err := esimDownloadExec(func(ctx context.Context, aidHex, smdp, matchingID, confirmationCode, imei string, progressFn esim.DownloadProgressFn) (esim.DownloadProfileResult, error) {
		gotIMEI = imei
		return esim.DownloadProfileResult{
			Warning:     "Profile 下载完成，但通知未完全确认",
			WarningCode: "download_notification_handle_failed",
		}, nil
	}, context.Background(), "A000", "example.com", "", "", "350225641234561", nil)
	if err != nil {
		t.Fatalf("esimDownloadExec() error=%v", err)
	}
	if result.WarningCode != "download_notification_handle_failed" {
		t.Fatalf("result=%#v want warning result passthrough", result)
	}
	if gotIMEI != "350225641234561" {
		t.Fatalf("imei=%q want forwarded custom IMEI", gotIMEI)
	}
}

func TestEsimDeleteExecPropagatesWarningResult(t *testing.T) {
	result, err := esimDeleteExec(func(string, string) (esim.DeleteProfileResult, error) {
		return esim.DeleteProfileResult{
			Warning:     "Profile 已删除，但删除通知发送未完全确认",
			WarningCode: "delete_notification_not_observed",
		}, nil
	}, "8986001234567890123", "A000")
	if err != nil {
		t.Fatalf("esimDeleteExec() error=%v", err)
	}
	if result.WarningCode != "delete_notification_not_observed" {
		t.Fatalf("result=%#v want warning result passthrough", result)
	}
}

func TestEsimNotificationsHTTPStatusMapsStructuredErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "busy", err: esim.ErrOperationInProgress, want: http.StatusConflict},
		{name: "invalid sequence", err: esim.NewNotificationError(esim.NotificationErrorInvalidSequence, "bad seq", nil), want: http.StatusBadRequest},
		{name: "invalid aid", err: esim.NewNotificationError(esim.NotificationErrorInvalidAIDHex, "bad aid", nil), want: http.StatusBadRequest},
		{name: "not found", err: esim.NewNotificationError(esim.NotificationErrorNotFound, "missing", nil), want: http.StatusNotFound},
		{name: "internal", err: esim.NewNotificationError(esim.NotificationErrorInternal, "boom", nil), want: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := esimNotificationHTTPStatus(tc.err); got != tc.want {
				t.Fatalf("status=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestEsimNotificationExecHelpersPropagateResults(t *testing.T) {
	items, err := esimNotificationListExec(func(string) ([]esim.NotificationItem, error) {
		return []esim.NotificationItem{{SequenceNumber: 11, Event: "install", ICCID: "8986001234567890123", Address: "install.example.com", AIDHex: "A000", CanRetry: true}}, nil
	}, "A000")
	if err != nil {
		t.Fatalf("esimNotificationListExec() error=%v", err)
	}
	if len(items) != 1 || items[0].SequenceNumber != 11 || items[0].Event != "install" {
		t.Fatalf("items=%#v want passthrough notification item", items)
	}
	if err := esimNotificationRetryExec(func(sequence int64, aidHex string) error {
		if sequence != 11 || aidHex != "A000" {
			t.Fatalf("sequence=%d aidHex=%q want 11/A000", sequence, aidHex)
		}
		return nil
	}, 11, "A000"); err != nil {
		t.Fatalf("esimNotificationRetryExec() error=%v", err)
	}
}

func TestHandleEsimNotificationListUsesJSONContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldExec := esimNotificationListExec
	defer func() { esimNotificationListExec = oldExec }()

	mgr := newTestEsimManager()
	p := device.NewPool(&config.Config{})
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-esim": {ID: "dev-esim", EsimMgr: mgr},
	})
	server := &Server{pool: p}

	esimNotificationListExec = func(_ func(string) ([]esim.NotificationItem, error), aidHex string) ([]esim.NotificationItem, error) {
		if aidHex != "A000" {
			t.Fatalf("aidHex=%q want A000", aidHex)
		}
		return []esim.NotificationItem{{SequenceNumber: 11, Event: "install", ICCID: "8986001234567890123", Address: "install.example.com", AIDHex: "A000", CanRetry: true}}, nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-esim"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/devices/dev-esim/esim/notifications?aid_hex=A000", nil)

	server.handleEsimListNotifications(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	if body := recorder.Body.String(); body == "" || !containsAll(body, `"items"`, `"sequence_number":11`, `"event":"install"`, `"aid_hex":"A000"`, `"can_retry":true`) {
		t.Fatalf("body=%q want notification list payload", body)
	}
}

func TestHandleEsimNotificationListPassesEmptyAidForCurrentCardDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldExec := esimNotificationListExec
	defer func() { esimNotificationListExec = oldExec }()

	mgr := newTestEsimManager()
	p := device.NewPool(&config.Config{})
	setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
		"dev-esim": {ID: "dev-esim", EsimMgr: mgr},
	})
	server := &Server{pool: p}

	esimNotificationListExec = func(_ func(string) ([]esim.NotificationItem, error), aidHex string) ([]esim.NotificationItem, error) {
		if aidHex != "" {
			t.Fatalf("aidHex=%q want empty current-card default", aidHex)
		}
		return []esim.NotificationItem{{SequenceNumber: 11, Event: "install", AIDHex: "A000", CanRetry: true}}, nil
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = gin.Params{{Key: "device_id", Value: "dev-esim"}}
	ctx.Request = httptest.NewRequest(http.MethodGet, "/devices/dev-esim/esim/notifications", nil)

	server.handleEsimListNotifications(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestHandleEsimRetryNotificationMapsStatusCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldExec := esimNotificationRetryExec
	defer func() { esimNotificationRetryExec = oldExec }()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantParts  []string
	}{
		{name: "success", err: nil, wantStatus: http.StatusOK, wantParts: []string{`"status":"ok"`, `"message":"通知重试发送成功"`}},
		{name: "busy", err: esim.ErrOperationInProgress, wantStatus: http.StatusConflict, wantParts: []string{`"busy":true`, `"code":"ESIM_BUSY"`, `"reason":"retry_notification"`}},
		{name: "invalid", err: esim.NewNotificationError(esim.NotificationErrorInvalidSequence, "bad seq", nil), wantStatus: http.StatusBadRequest, wantParts: []string{`bad seq`}},
		{name: "not found", err: esim.NewNotificationError(esim.NotificationErrorNotFound, "missing", nil), wantStatus: http.StatusNotFound, wantParts: []string{`missing`}},
		{name: "internal", err: esim.NewNotificationError(esim.NotificationErrorInternal, "boom", nil), wantStatus: http.StatusInternalServerError, wantParts: []string{`boom`}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newTestEsimManager()
			p := device.NewPool(&config.Config{})
			setNestedPrivateField(t, p, []string{"workers"}, map[string]*device.Worker{
				"dev-esim": {ID: "dev-esim", EsimMgr: mgr},
			})
			server := &Server{pool: p}
			esimNotificationRetryExec = func(_ func(int64, string) error, sequence int64, aidHex string) error {
				if sequence != 11 || aidHex != "A000" {
					t.Fatalf("sequence=%d aidHex=%q want 11/A000", sequence, aidHex)
				}
				return tc.err
			}

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Params = gin.Params{{Key: "device_id", Value: "dev-esim"}, {Key: "sequence", Value: "11"}}
			ctx.Request = httptest.NewRequest(http.MethodPost, "/devices/dev-esim/esim/notifications/11/actions/retry?aid_hex=A000", nil)

			server.handleEsimRetryNotification(ctx)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantStatus == http.StatusConflict {
				if got := recorder.Header().Get("Retry-After"); got != "2" {
					t.Fatalf("Retry-After=%q want=2", got)
				}
			}
			if body := recorder.Body.String(); body == "" || !containsAll(body, tc.wantParts...) {
				t.Fatalf("body=%q want parts=%v", body, tc.wantParts)
			}
		})
	}
}
