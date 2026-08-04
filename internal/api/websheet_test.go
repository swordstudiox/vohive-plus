package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/websheet"
)

func TestRespondWebsheetErrorMapsStatuses(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: websheet.ErrNotFound, want: http.StatusNotFound},
		{err: websheet.ErrExpired, want: http.StatusGone},
		{err: websheet.ErrUnsafeURL, want: http.StatusBadRequest},
		{err: websheet.ErrUnauthorized, want: http.StatusUnauthorized},
		{err: errors.New("boom"), want: http.StatusInternalServerError},
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		respondWebsheetError(c, tt.err)
		if rec.Code != tt.want {
			t.Fatalf("respondWebsheetError(%v)=%d want %d", tt.err, rec.Code, tt.want)
		}
	}
}

func TestWebsheetBootstrapUsesSessionTokenOutsideGlobalAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	broker := websheet.New(websheet.Config{AllowPrivateHosts: true})
	session, err := broker.Create(context.Background(), websheet.Request{URL: "https://203.0.113.10/start"})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{
		auth:      config.WebConfig{Password: "secret"},
		websheets: broker,
	}
	router := gin.New()
	api := router.Group("/api")
	server.registerWebsheetRoutes(api)
	api.Use(server.authMiddleware())
	api.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	valid := httptest.NewRecorder()
	validReq := httptest.NewRequest(http.MethodGet, session.Info().EmbedURL, nil)
	router.ServeHTTP(valid, validReq)
	if valid.Code == http.StatusUnauthorized {
		t.Fatalf("bootstrap with websheet token returned auth 401: %s", valid.Body.String())
	}
	if valid.Code != http.StatusFound {
		t.Fatalf("bootstrap with websheet token status=%d want %d body=%s", valid.Code, http.StatusFound, valid.Body.String())
	}

	missing := httptest.NewRecorder()
	missingReq := httptest.NewRequest(http.MethodGet, "/api/websheets/"+session.Info().ID, nil)
	router.ServeHTTP(missing, missingReq)
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap without websheet token status=%d want %d body=%s", missing.Code, http.StatusUnauthorized, missing.Body.String())
	}

	protected := httptest.NewRecorder()
	protectedReq := httptest.NewRequest(http.MethodGet, "/api/protected", nil)
	router.ServeHTTP(protected, protectedReq)
	if protected.Code != http.StatusUnauthorized {
		t.Fatalf("protected route status=%d want %d", protected.Code, http.StatusUnauthorized)
	}
}

func TestWebsheetCallbackMarksTerminalSessionDone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	broker := websheet.New(websheet.Config{AllowPrivateHosts: true})
	session, err := broker.Create(context.Background(), websheet.Request{URL: "https://203.0.113.10/start"})
	if err != nil {
		t.Fatal(err)
	}

	server := &Server{websheets: broker}
	router := gin.New()
	api := router.Group("/api")
	server.registerWebsheetRoutes(api)

	req := httptest.NewRequest(http.MethodPost, "/api/websheets/"+session.Info().ID+"/callback?token="+websheetToken(session), strings.NewReader(`{
		"source":"vowifi",
		"event":"entitlementChanged",
		"method":"e911AddressValidated",
		"resultCode":"success"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("callback status=%d body=%s", rec.Code, rec.Body.String())
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := session.WaitDone(waitCtx); err != nil {
		t.Fatalf("session was not marked done after terminal callback: %v", err)
	}
}

func websheetToken(session *websheet.Session) string {
	parts := strings.SplitN(session.Info().EmbedURL, "token=", 2)
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}
