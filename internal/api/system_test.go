package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

// resolveUninstallTargets 必须使用运行时实际加载的配置文件路径，
// 而不是硬编码相对路径 "config"——后者在 OpenWrt 部署下（通过 -c 传入
// /etc/vohive/config.yaml，与进程工作目录无关）永远指向一个不存在的目录，
// 导致真实配置文件从未被清理。
func TestResolveUninstallTargetsUsesActualConfigPath(t *testing.T) {
	dataDir, configFile := resolveUninstallTargets("/etc/vohive/config.yaml")

	if dataDir != "data" {
		t.Fatalf("dataDir = %q, want %q", dataDir, "data")
	}
	if configFile != "/etc/vohive/config.yaml" {
		t.Fatalf("configFile = %q, want %q", configFile, "/etc/vohive/config.yaml")
	}
}

func TestResolveUninstallTargetsSkipsConfigWhenPathUnknown(t *testing.T) {
	// 配置管理器尚未初始化时 config.GetConfigPath() 返回空字符串，
	// 此时绝不能删除任何路径（避免误删进程当前工作目录）。
	_, configFile := resolveUninstallTargets("")

	if configFile != "" {
		t.Fatalf("configFile = %q, want empty when config path unknown", configFile)
	}
}

// detectServiceStopCommands 决定自毁前应主动通知哪个服务管理器停止+禁用自启，
// 这样即使删除可执行文件失败（例如只读文件系统），supervisor 也不会把进程重新拉起来，
// 而不是像原来那样仅依赖"删掉自己导致 exec 失败"这种脆弱的副作用。
func TestDetectServiceStopCommandsPrefersOpenWrtInitScript(t *testing.T) {
	statFile := func(path string) bool { return path == "/etc/init.d/vohive" }
	lookPath := func(name string) (string, error) { return "/usr/bin/systemctl", nil }

	cmds := detectServiceStopCommands(lookPath, statFile)

	if len(cmds) != 2 {
		t.Fatalf("len(cmds) = %d, want 2 (disable + stop), got %v", len(cmds), cmds)
	}
	if cmds[0][0] != "/etc/init.d/vohive" || cmds[0][1] != "disable" {
		t.Fatalf("cmds[0] = %v, want [/etc/init.d/vohive disable]", cmds[0])
	}
	if cmds[1][0] != "/etc/init.d/vohive" || cmds[1][1] != "stop" {
		t.Fatalf("cmds[1] = %v, want [/etc/init.d/vohive stop]", cmds[1])
	}
}

func TestDetectServiceStopCommandsFallsBackToSystemd(t *testing.T) {
	statFile := func(path string) bool { return false }
	lookPath := func(name string) (string, error) {
		if name == "systemctl" {
			return "/usr/bin/systemctl", nil
		}
		return "", errNotFound
	}

	cmds := detectServiceStopCommands(lookPath, statFile)

	if len(cmds) != 1 {
		t.Fatalf("len(cmds) = %d, want 1, got %v", len(cmds), cmds)
	}
	want := []string{"systemctl", "disable", "--now", "vohive"}
	if len(cmds[0]) != len(want) {
		t.Fatalf("cmds[0] = %v, want %v", cmds[0], want)
	}
	for i := range want {
		if cmds[0][i] != want[i] {
			t.Fatalf("cmds[0] = %v, want %v", cmds[0], want)
		}
	}
}

func TestDetectServiceStopCommandsEmptyWhenNoSupervisorDetected(t *testing.T) {
	statFile := func(path string) bool { return false }
	lookPath := func(name string) (string, error) { return "", errNotFound }

	cmds := detectServiceStopCommands(lookPath, statFile)

	if len(cmds) != 0 {
		t.Fatalf("cmds = %v, want empty when neither systemd nor openwrt detected", cmds)
	}
}

func TestHandleUninstallRejectsUnauthenticatedRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{auth: config.WebConfig{Username: "admin", Password: "secret"}}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/system/uninstall", nil)

	s.handleUninstall(c)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d body=%s, want 401 —— uninstall 必须要求登录后才能调用", w.Code, w.Body.String())
	}
}

func TestNewRouterRequiresAuthForUninstall(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{auth: config.WebConfig{Username: "admin", Password: "secret"}}
	r := s.newRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/system/uninstall", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d body=%s, want 401", w.Code, w.Body.String())
	}
}

func TestHandleApplyUpdateReportsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{auth: config.WebConfig{Username: "admin", Password: "secret"}}
	r := s.newRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/system/update/apply", nil)
	req.Header.Set("Authorization", "Bearer "+testSessionToken(t, "secret", time.Now().Add(time.Hour)))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("code=%d body=%s, want 409 when updater is disabled", w.Code, w.Body.String())
	}
}

func testSessionToken(t *testing.T, password string, exp time.Time) string {
	t.Helper()
	expStr := strconv.FormatInt(exp.Unix(), 10)
	h := hmac.New(sha256.New, []byte(password))
	h.Write([]byte(expStr))
	sig := hex.EncodeToString(h.Sum(nil))
	return base64.StdEncoding.EncodeToString([]byte(expStr + "." + sig))
}
