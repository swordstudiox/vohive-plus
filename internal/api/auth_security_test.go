package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
)

// 登录限流必须按真实客户端 IP 计数，而不是按客户端可随意伪造的
// X-Forwarded-For 头计数，否则暴力破解限流形同虚设。
func TestLoginRateLimitIgnoresSpoofedForwardedFor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{
		auth:          config.WebConfig{Username: "admin", Password: "correct-horse-battery-staple"},
		loginAttempts: make(map[string]loginAttempt),
	}
	r := s.newRouter()

	body := `{"username":"admin","password":"wrong"}`

	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i)) // 每次伪造不同的来源 IP
		req.RemoteAddr = "192.0.2.1:54321"                             // 真实连接对端始终相同
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code=%d want 401, body=%s", i, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "10.0.0.99") // 第 11 次，依然是新的伪造 IP
	req.RemoteAddr = "192.0.2.1:54321"
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: code=%d want 429 —— 限流应按真实 RemoteAddr 计数，不应被伪造的 X-Forwarded-For 绕过, body=%s", w.Code, w.Body.String())
	}
}

func TestCheckPasswordPlaintextFallbackStillMatchesCorrectly(t *testing.T) {
	if !checkPassword("plain-secret", "plain-secret") {
		t.Fatal("相同的明文密码应当匹配")
	}
	if checkPassword("plain-secret", "wrong-secret") {
		t.Fatal("不同明文密码不应匹配")
	}
	if checkPassword("plain-secret", "plain-secre") {
		t.Fatal("长度不同的明文密码不应匹配")
	}
	if checkPassword("plain-secret", "") {
		t.Fatal("空密码不应匹配非空存储密码")
	}
}
