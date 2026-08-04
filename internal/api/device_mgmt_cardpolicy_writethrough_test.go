package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/device"
)

func TestCardPolicyFromDeviceConfigMapping(t *testing.T) {
	cfg := config.DeviceConfig{
		NetworkEnabled: true, VoWiFiEnabled: true, IPVersion: "v4v6", APN: "ims",
	}
	pol := cardPolicyFromDeviceConfig("ICC9", cfg)
	if pol.ICCID != "ICC9" || !pol.NetworkEnabled || !pol.VoWiFiEnabled {
		t.Fatalf("映射错: %+v", pol)
	}
	if pol.IPVersion != "v4v6" || pol.APN != "ims" || pol.Source != "user" {
		t.Fatalf("映射错: %+v", pol)
	}
}

// 策略跟卡走后，保存设备配置必须是策略中性的：
// 不得用 DTO 回传的（GET config 恒零的）策略字段覆盖 card_policies。
// 策略只能经 PUT /cards/:iccid/policy 修改。
func TestUpdateDeviceDoesNotClobberCardPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	initPolicyTestDB(t)

	iccid := "8986008888888888888"
	if err := db.DB.Create(&db.Device{IMEI: "imei-x", Alias: "wwan9", CurrentICCID: &iccid}).Error; err != nil {
		t.Fatal(err)
	}

	// 预置一条用户态卡策略：vowifi 开启、用户飞行意图开启、network 关闭、双栈。
	if err := db.UpsertCardPolicy(db.CardPolicy{
		ICCID: iccid, VoWiFiEnabled: true, AirplaneEnabled: true, IPVersion: "v4v6", APN: "ims", Source: "user",
	}); err != nil {
		t.Fatal(err)
	}

	path := writeDeviceMgmtLimitConfig(t, `
server:
  port: ":7575"
devices:
  - id: wwan9
    interface: wwan9
`)
	if err := config.InitGlobalManager(path); err != nil {
		t.Fatalf("InitGlobalManager() error = %v", err)
	}
	server := &Server{pool: device.NewPool(&config.Config{}), configPath: path}

	// 仅改设备名称；DTO 不携带策略（等价于前端配置页不再编辑策略，策略字段恒零）。
	// 旧实现会把 network=false/vowifi=false/ip="" 透写从而清空卡策略。
	body := `{"config":{"id":"wwan9","name":"renamed"}}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = gin.Params{{Key: "device_id", Value: "wwan9"}}
	ctx.Request = httptest.NewRequest(http.MethodPut, "/devices/wwan9", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	server.handleDeviceMgmtUpdateDevice(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	// 卡策略必须原样保留，未被设备保存清空。
	got, err := db.GetCardPolicy(iccid)
	if err != nil {
		t.Fatalf("卡策略丢失: %v", err)
	}
	if !got.VoWiFiEnabled || !got.AirplaneEnabled {
		t.Fatalf("卡策略被设备保存清空: %+v", got)
	}
	if got.NetworkEnabled {
		t.Fatalf("不变式被破坏(vowifi 开应 network 关): %+v", got)
	}
	if got.IPVersion != "v4v6" || got.APN != "ims" {
		t.Fatalf("卡策略内容被改写: %+v", got)
	}
}
