package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/db"
	"github.com/swordstudiox/vohive-plus/internal/upstreamproxy"
)

// ── 前置代理管理 API（主服务） ──

func normalizeUpstreamProxyPayload(existing *db.UpstreamProxy, req db.UpstreamProxy) db.UpstreamProxy {
	out := req
	out.ID = strings.TrimSpace(out.ID)
	out.Name = strings.TrimSpace(out.Name)
	out.Addr = strings.TrimSpace(out.Addr)
	out.Username = strings.TrimSpace(out.Username)
	out.Password = strings.TrimSpace(out.Password)

	if existing != nil {
		out.CreatedAt = existing.CreatedAt
		if out.Password == "" {
			out.Password = existing.Password
		}
	}
	return out
}

func probeUpstreamProxyConfig(c *gin.Context, proxy db.UpstreamProxy) (upstreamproxy.ProbeResult, error) {
	return upstreamproxy.ProbeSOCKS5(c.Request.Context(), upstreamproxy.ProbeConfig{
		ProxyAddr: proxy.Addr,
		Username:  proxy.Username,
		Password:  proxy.Password,
		Timeout:   5 * time.Second,
	})
}

// handleListUpstreamProxies 获取所有前置代理实例
func (s *Server) handleListUpstreamProxies(c *gin.Context) {
	proxies, err := db.ListUpstreamProxies()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	// 密码脱敏
	for i := range proxies {
		proxies[i].Password = maskSecret(proxies[i].Password)
	}
	c.JSON(http.StatusOK, proxies)
}

// handleCreateUpstreamProxy 创建前置代理实例
func (s *Server) handleCreateUpstreamProxy(c *gin.Context) {
	var req db.UpstreamProxy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数解析失败: " + err.Error()})
		return
	}
	req = normalizeUpstreamProxyPayload(nil, req)
	if req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "id 不能为空"})
		return
	}
	if req.Addr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "addr 不能为空"})
		return
	}
	result, probeErr := probeUpstreamProxyConfig(c, req)
	if probeErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"status":  "error",
			"message": "前置代理探测失败: " + result.FailureSummary(),
			"result":  result,
		})
		return
	}
	if err := db.UpsertUpstreamProxy(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "前置代理已保存，并已通过探测",
		"result":  result,
	})
}

// handleUpdateUpstreamProxy 更新前置代理实例
func (s *Server) handleUpdateUpstreamProxy(c *gin.Context) {
	id := upstreamProxyIDParam(c)
	var req db.UpstreamProxy
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数解析失败: " + err.Error()})
		return
	}
	existing, err := db.GetUpstreamProxyByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	if existing == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "前置代理不存在"})
		return
	}
	req.ID = id
	req = normalizeUpstreamProxyPayload(existing, req)
	if req.Addr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "addr 不能为空"})
		return
	}
	result, probeErr := probeUpstreamProxyConfig(c, req)
	if probeErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"status":  "error",
			"message": "前置代理探测失败: " + result.FailureSummary(),
			"result":  result,
		})
		return
	}
	if err := db.UpsertUpstreamProxy(req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "前置代理已更新，并已通过探测",
		"result":  result,
	})
}

// handleDeleteUpstreamProxy 删除前置代理实例
func (s *Server) handleDeleteUpstreamProxy(c *gin.Context) {
	id := upstreamProxyIDParam(c)
	if err := db.DeleteUpstreamProxy(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "前置代理已删除"})
}

// handleProbeUpstreamProxy 探测前置代理是否支持标准 Socks5 + UDP Associate。
func (s *Server) handleProbeUpstreamProxy(c *gin.Context) {
	id := upstreamProxyIDParam(c)
	proxy, err := db.GetUpstreamProxyByID(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	if proxy == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "前置代理不存在"})
		return
	}

	result, probeErr := upstreamproxy.ProbeSOCKS5(c.Request.Context(), upstreamproxy.ProbeConfig{
		ProxyAddr: proxy.Addr,
		Username:  proxy.Username,
		Password:  proxy.Password,
		Timeout:   5 * time.Second,
	})
	if probeErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"status":  "error",
			"message": "前置代理探测失败: " + result.FailureSummary(),
			"result":  result,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "前置代理探测成功",
		"result":  result,
	})
}

type upstreamProxyCountryRuleResponse struct {
	CountryCode     string    `json:"country_code"`
	CountryName     string    `json:"country_name"`
	MCCs            []string  `json:"mccs"`
	UpstreamProxyID string    `json:"upstream_proxy_id"`
	Enabled         bool      `json:"enabled"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func buildUpstreamProxyCountryRuleResponse(rule db.UpstreamProxyCountryRule) upstreamProxyCountryRuleResponse {
	display := upstreamproxy.CountryRuleDisplay(rule.CountryCode)
	return upstreamProxyCountryRuleResponse{
		CountryCode:     display.CountryCode,
		CountryName:     display.CountryName,
		MCCs:            display.MCCs,
		UpstreamProxyID: strings.TrimSpace(rule.UpstreamProxyID),
		Enabled:         rule.Enabled,
		UpdatedAt:       rule.UpdatedAt,
	}
}

func (s *Server) handleListUpstreamProxyCountries(c *gin.Context) {
	if !upstreamproxy.CountryTableReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mcc_mnc_table_unavailable"})
		return
	}
	c.JSON(http.StatusOK, upstreamproxy.ListCountryDisplays())
}

func (s *Server) handleListUpstreamProxyCountryRules(c *gin.Context) {
	rules, err := db.ListUpstreamProxyCountryRules()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	out := make([]upstreamProxyCountryRuleResponse, 0, len(rules))
	for _, rule := range rules {
		out = append(out, buildUpstreamProxyCountryRuleResponse(rule))
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleUpsertUpstreamProxyCountryRule(c *gin.Context) {
	if !upstreamproxy.CountryTableReady() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "mcc_mnc_table_unavailable"})
		return
	}
	countryCode := upstreamproxy.NormalizeCountryCode(countryCodeParam(c))
	if _, ok := upstreamproxy.MCCsForCountryCode(countryCode); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "国家代码不在 MCC/MNC 表中"})
		return
	}
	var req struct {
		UpstreamProxyID string `json:"upstream_proxy_id"`
		Enabled         bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数解析失败: " + err.Error()})
		return
	}
	proxy, err := db.GetUpstreamProxyByID(req.UpstreamProxyID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	if proxy == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "前置代理不存在"})
		return
	}
	rule := db.UpstreamProxyCountryRule{
		CountryCode:     countryCode,
		UpstreamProxyID: proxy.ID,
		Enabled:         req.Enabled,
	}
	if err := db.UpsertUpstreamProxyCountryRule(rule); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	rule.UpstreamProxyID = proxy.ID
	rule.CountryCode = countryCode
	c.JSON(http.StatusOK, buildUpstreamProxyCountryRuleResponse(rule))
}

func (s *Server) handleDeleteUpstreamProxyCountryRule(c *gin.Context) {
	countryCode := upstreamproxy.NormalizeCountryCode(countryCodeParam(c))
	if err := db.DeleteUpstreamProxyCountryRule(countryCode); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// maskSecret 将密码脱敏为 **** 格式
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return "****"
}
