package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/proxy/server"
	"github.com/swordstudiox/vohive-plus/pkg/logger"

	"github.com/gin-gonic/gin"
)

// proxyOverviewResponse API 响应：代理配置概览
type proxyOverviewResponse struct {
	Instances []proxyInstanceDTO      `json:"instances"`
	Devices   []proxyDeviceDTO        `json:"devices"`
	Status    []server.InstanceStatus `json:"status"`
}

// proxyInstanceDTO 代理实例 DTO
type proxyInstanceDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DeviceID    string `json:"device_id"`
	Enabled     bool   `json:"enabled"`
	Mode        string `json:"mode"`
	ListenAddr  string `json:"listen_addr"`
	ListenPort  int    `json:"listen_port"`
	AuthEnabled bool   `json:"auth_enabled"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
}

// proxyDeviceDTO 设备 DTO
type proxyDeviceDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Interface string `json:"interface"`
}

// proxyConfigRequest 更新代理配置请求
type proxyConfigRequest struct {
	Instances []config.ProxyInstance `json:"instances"`
}

// handleProxyOverview 获取代理配置概览
func (s *Server) handleProxyOverview(c *gin.Context) {
	ctx := c.Request.Context()
	resp := proxyOverviewResponse{
		Instances: make([]proxyInstanceDTO, 0),
		Devices:   make([]proxyDeviceDTO, 0),
		Status:    nil,
	}

	instances, err := s.proxyRepo.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "加载实例失败: " + err.Error()})
		return
	}
	resp.Instances = make([]proxyInstanceDTO, 0, len(instances))
	for _, inst := range instances {
		resp.Instances = append(resp.Instances, instanceToDTO(inst, true))
	}

	{
		managed := config.ListDevices()
		resp.Devices = make([]proxyDeviceDTO, 0, len(managed))
		for _, d := range managed {
			name := d.Name
			if name == "" {
				name = d.ID
			}
			resp.Devices = append(resp.Devices, proxyDeviceDTO{
				ID:        d.ID,
				Name:      name,
				Interface: d.Interface,
			})
		}
	}

	if s.proxyMgr != nil {
		resp.Status = s.proxyMgr.ListStatus()
	}

	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleProxyInstanceGet(c *gin.Context) {
	ctx := c.Request.Context()
	id := proxyInstanceIDParam(c)
	inst, err := s.proxyRepo.Get(ctx, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "加载实例失败: " + err.Error()})
		return
	}
	if inst == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "实例不存在: " + id})
		return
	}
	c.JSON(http.StatusOK, instanceToDTO(*inst, false))
}

// handleProxyUpdateConfig 更新代理配置
func (s *Server) handleProxyUpdateConfig(c *gin.Context) {
	ctx := c.Request.Context()
	var req proxyConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "参数错误: " + err.Error()})
		return
	}

	oldInstMap := make(map[string]config.ProxyInstance)
	oldInstances, err := s.proxyRepo.List(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "读取旧配置失败: " + err.Error()})
		return
	}
	for _, oldInst := range oldInstances {
		oldInstMap[oldInst.ID] = oldInst
	}

	normalizedInstances := make([]config.ProxyInstance, 0, len(req.Instances))
	for _, inst := range req.Instances {
		var oldInst *config.ProxyInstance
		if old, ok := oldInstMap[inst.ID]; ok {
			oldCopy := old
			oldInst = &oldCopy
		}
		normalized, err := normalizeProxyInstanceForSave(inst, oldInst)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": err.Error()})
			return
		}
		normalizedInstances = append(normalizedInstances, normalized)
	}

	if err := s.proxyRepo.ReplaceAll(ctx, normalizedInstances); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "写入数据库失败: " + err.Error()})
		return
	}

	if err := s.SyncProxyConfigs(); err != nil {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "applied": false, "warning": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "applied": true})
}

func restoreProxySecrets(inst *config.ProxyInstance, oldInst config.ProxyInstance) {
	if inst == nil {
		return
	}
	if inst.Password == "******" {
		inst.Password = oldInst.Password
	}
}

func normalizeProxyInstanceForSave(inst config.ProxyInstance, oldInst *config.ProxyInstance) (config.ProxyInstance, error) {
	if strings.TrimSpace(inst.ID) == "" {
		return config.ProxyInstance{}, errors.New("实例 id 不能为空")
	}
	if strings.TrimSpace(inst.DeviceID) == "" {
		return config.ProxyInstance{}, errors.New("device_id 不能为空")
	}
	mode, err := normalizeProxyMode(inst.Mode)
	if err != nil {
		return config.ProxyInstance{}, err
	}
	inst.Mode = mode
	if inst.ListenPort <= 0 || inst.ListenPort > 65535 {
		return config.ProxyInstance{}, errors.New("listen_port 无效")
	}
	if strings.TrimSpace(inst.ListenAddr) == "" {
		inst.ListenAddr = "0.0.0.0"
	}
	if oldInst != nil {
		restoreProxySecrets(&inst, *oldInst)
	}
	if !inst.AuthEnabled {
		inst.Username = ""
		inst.Password = ""
		return inst, nil
	}
	inst.Username = strings.TrimSpace(inst.Username)
	inst.Password = strings.TrimSpace(inst.Password)
	if inst.Username == "" || inst.Password == "" {
		return config.ProxyInstance{}, errors.New("启用认证时 username/password 不能为空")
	}
	return inst, nil
}

func normalizeProxyMode(mode string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "" {
		return server.ModeSocks5, nil
	}
	switch m {
	case server.ModeSocks5, server.ModeHTTP:
		return m, nil
	default:
		return "", errors.New("mode 仅支持 socks5 或 http")
	}
}

// handleProxyInstanceStart 启动代理实例
func (s *Server) handleProxyInstanceStart(c *gin.Context) {
	id := proxyInstanceIDParam(c)
	if s.proxyMgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "代理管理未启用"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.proxyMgr.Start(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleProxyInstanceStop 停止代理实例
func (s *Server) handleProxyInstanceStop(c *gin.Context) {
	id := proxyInstanceIDParam(c)
	if s.proxyMgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "代理管理未启用"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.proxyMgr.Stop(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleProxyInstanceRestart 重启代理实例
func (s *Server) handleProxyInstanceRestart(c *gin.Context) {
	id := proxyInstanceIDParam(c)
	if s.proxyMgr == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "代理管理未启用"})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.proxyMgr.Restart(ctx, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// SyncProxyConfigs 同步代理配置到实例管理器
func (s *Server) SyncProxyConfigs() error {
	if s.proxyMgr == nil {
		return nil
	}
	s.proxySyncMu.Lock()
	defer s.proxySyncMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cfgs, err := s.buildProxyConfigs(ctx)
	if err != nil {
		return err
	}
	return s.proxyMgr.ApplyConfigs(ctx, cfgs)
}

func (s *Server) buildProxyConfigs(ctx context.Context) ([]server.InstanceConfig, error) {
	deviceInterface := make(map[string]string)
	{
		managed := config.ListDevices()
		for _, d := range managed {
			iface := strings.TrimSpace(d.Interface)
			if iface != "" {
				deviceInterface[d.ID] = iface
			}
		}
	}
	for _, w := range s.pool.GetAllWorkers() {
		if w == nil {
			continue
		}
		iface := strings.TrimSpace(w.Config.Interface)
		if iface != "" {
			deviceInterface[w.ID] = iface
		}
	}

	instances, err := s.proxyRepo.List(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]server.InstanceConfig, 0, len(instances))
	for _, inst := range instances {
		if strings.TrimSpace(inst.DeviceID) == "" {
			return nil, errors.New("device_id 不能为空")
		}
		mode, err := normalizeProxyMode(inst.Mode)
		if err != nil {
			return nil, err
		}
		inst.Mode = mode
		if strings.TrimSpace(inst.ListenAddr) == "" {
			inst.ListenAddr = "0.0.0.0"
		}
		if inst.ListenPort <= 0 || inst.ListenPort > 65535 {
			return nil, errors.New("listen_port 无效")
		}
		if inst.AuthEnabled {
			if strings.TrimSpace(inst.Username) == "" || strings.TrimSpace(inst.Password) == "" {
				return nil, errors.New("启用认证时 username/password 不能为空")
			}
		}

		iface := strings.TrimSpace(deviceInterface[inst.DeviceID])
		if iface == "" {
			return nil, errors.New("device_id 无效: " + inst.DeviceID)
		}

		logger.Info("代理实例绑定信息",
			"instance_id", inst.ID,
			"mode", inst.Mode,
			"device_id", inst.DeviceID,
			"bind_interface", iface,
		)

		out = append(out, server.InstanceConfig{
			ID:          inst.ID,
			Mode:        inst.Mode,
			Enabled:     inst.Enabled,
			ListenAddr:  inst.ListenAddr,
			ListenPort:  inst.ListenPort,
			Interface:   iface,
			AuthEnabled: inst.AuthEnabled,
			Username:    inst.Username,
			Password:    inst.Password,
		})
	}
	return out, nil
}

func instanceToDTO(inst config.ProxyInstance, mask bool) proxyInstanceDTO {
	mode := strings.ToLower(strings.TrimSpace(inst.Mode))
	if mode == "" {
		mode = server.ModeSocks5
	}
	return proxyInstanceDTO{
		ID:          inst.ID,
		Name:        inst.Name,
		DeviceID:    inst.DeviceID,
		Enabled:     inst.Enabled,
		Mode:        mode,
		ListenAddr:  inst.ListenAddr,
		ListenPort:  inst.ListenPort,
		AuthEnabled: inst.AuthEnabled,
		Username:    inst.Username,
		Password:    maybeMaskSecret(inst.Password, mask),
	}
}

func maybeMaskSecret(v string, mask bool) string {
	if !mask {
		return v
	}
	if strings.TrimSpace(v) == "" {
		return ""
	}
	return "******"
}
