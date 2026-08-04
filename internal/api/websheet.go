package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/websheet"
)

func (s *Server) registerWebsheetRoutes(api *gin.RouterGroup) {
	api.GET("/websheets/:id", s.handleWebsheetBootstrap)
	api.GET("/websheets/:id/proxy", s.handleWebsheetProxy)
	api.POST("/websheets/:id/proxy", s.handleWebsheetProxy)
	api.PUT("/websheets/:id/proxy", s.handleWebsheetProxy)
	api.PATCH("/websheets/:id/proxy", s.handleWebsheetProxy)
	api.DELETE("/websheets/:id/proxy", s.handleWebsheetProxy)
	api.GET("/websheets/:id/proxy/*target", s.handleWebsheetProxy)
	api.POST("/websheets/:id/proxy/*target", s.handleWebsheetProxy)
	api.PUT("/websheets/:id/proxy/*target", s.handleWebsheetProxy)
	api.PATCH("/websheets/:id/proxy/*target", s.handleWebsheetProxy)
	api.DELETE("/websheets/:id/proxy/*target", s.handleWebsheetProxy)
	api.POST("/websheets/:id/callback", s.handleWebsheetCallback)
	api.POST("/websheets/:id/done", s.handleWebsheetDone)
}

func (s *Server) websheetSession(c *gin.Context) (*websheet.Session, error) {
	if s.websheets == nil {
		return nil, websheet.ErrNotFound
	}
	return s.websheets.Get(c.Param("id"))
}

func (s *Server) authorizedWebsheetSession(c *gin.Context) (*websheet.Session, error) {
	session, err := s.websheetSession(c)
	if err != nil {
		return nil, err
	}
	if err := session.Authorize(c.Request); err != nil {
		if errors.Is(err, websheet.ErrUnauthorized) && s.isAuthenticatedRequest(c, time.Now()) {
			return session, nil
		}
		return nil, err
	}
	return session, nil
}

func (s *Server) handleWebsheetBootstrap(c *gin.Context) {
	session, err := s.authorizedWebsheetSession(c)
	if err != nil {
		respondWebsheetError(c, err)
		return
	}
	if err := session.ServeBootstrap(c.Writer, c.Request); err != nil {
		respondWebsheetError(c, err)
	}
}

func (s *Server) handleWebsheetProxy(c *gin.Context) {
	session, err := s.authorizedWebsheetSession(c)
	if err != nil {
		respondWebsheetError(c, err)
		return
	}
	if err := session.Proxy(c.Writer, c.Request); err != nil {
		respondWebsheetError(c, err)
	}
}

func (s *Server) handleWebsheetCallback(c *gin.Context) {
	session, err := s.authorizedWebsheetSession(c)
	if err != nil {
		respondWebsheetError(c, err)
		return
	}
	var callback websheet.Callback
	if err := c.ShouldBindJSON(&callback); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "websheet_callback_invalid", "message": err.Error()})
		return
	}
	session.Callback(callback)
	if isTerminalWebsheetCallback(callback) {
		session.Done()
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func isTerminalWebsheetCallback(callback websheet.Callback) bool {
	value := strings.ToLower(strings.TrimSpace(firstNonEmpty(callback.Event, callback.Method, callback.ResultCode)))
	if value == "" {
		return true
	}
	return !strings.Contains(value, "phoneservicesaccountstatuschanged")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *Server) handleWebsheetDone(c *gin.Context) {
	session, err := s.authorizedWebsheetSession(c)
	if err != nil {
		respondWebsheetError(c, err)
		return
	}
	session.Done()
	if s.websheets != nil {
		s.websheets.Delete(c.Param("id"))
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func respondWebsheetError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, websheet.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "code": "websheet_not_found", "message": err.Error()})
	case errors.Is(err, websheet.ErrExpired):
		c.JSON(http.StatusGone, gin.H{"status": "error", "code": "websheet_expired", "message": err.Error()})
	case errors.Is(err, websheet.ErrUnsafeURL):
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "code": "websheet_unsafe_url", "message": err.Error()})
	case errors.Is(err, websheet.ErrUnauthorized):
		c.JSON(http.StatusUnauthorized, gin.H{"status": "error", "code": "websheet_unauthorized", "message": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "code": "websheet_proxy_failed", "message": err.Error()})
	}
}
