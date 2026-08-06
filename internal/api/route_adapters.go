package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/swordstudiox/vohive-plus/internal/db"
)

type enabledPatchRequest struct {
	Enabled *bool `json:"enabled"`
}

type networkPatchRequest struct {
	Enabled   *bool  `json:"enabled"`
	IPVersion string `json:"ip_version"`
	APN       string `json:"apn"`
}

func (s *Server) handleDeviceNetworkPatch(c *gin.Context) {
	var req networkPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "enabled 为必填项"})
		return
	}

	deviceID := deviceIDParam(c)

	if *req.Enabled {
		if s.pool == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "message": "服务未就绪"})
			return
		}
		worker := s.pool.GetWorker(deviceID)
		if worker == nil {
			c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "设备未找到或未运行"})
			return
		}
		nc := worker.NetworkController()
		if nc == nil {
			c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "当前设备不支持网络控制"})
			return
		}
		if s.pool.IsVoWiFiActive(deviceID) {
			c.JSON(http.StatusConflict, gin.H{"status": "error", "message": "VoWiFi 运行中，无法启动数据网络"})
			return
		}
		// 落库：network_enabled=true + ip_version + apn（APN/IP 供下次连接生效）
		ipVersion := strings.TrimSpace(req.IPVersion)
		apn := strings.TrimSpace(req.APN)
		patch, ok := s.patchCardPolicyForDeviceTxOrRespond(c, deviceID, "SIM 身份未确认，请等待切卡完成后再开启蜂窝数据", func(p *db.CardPolicy) {
			p.NetworkEnabled = true
			if ipVersion != "" {
				p.IPVersion = ipVersion
			}
			p.APN = apn
		})
		if !ok {
			return
		}
		// 同步 w.Config，使概览读到最新值（QMI APN 在下次连接时生效）
		if patch.ICCID != "" {
			s.pool.SetWorkerNetworkPolicy(deviceID, true, ipVersion, apn)
		}
		if err := worker.StartNetwork(); err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "data_roaming_disabled") {
				status = http.StatusConflict
			}
			c.JSON(status, gin.H{"status": "error", "message": "启动数据网络失败: " + err.Error() + rollbackCardPolicyPatchMessage(patch)})
			return
		}
		go func() { _ = worker.RefreshRuntime(nil, "start_network") }()
		c.JSON(http.StatusOK, gin.H{
			"status":            "ok",
			"message":           "数据网络已启动",
			"device":            deviceID,
			"network_connected": worker.NetworkConnected(),
			"private_ip":        nc.GetPrivateIP(),
			"private_ipv6":      nc.GetPrivateIPv6(),
			"public_ip":         worker.GetCachedIP(),
			"public_ipv6":       worker.GetCachedIPv6(),
		})
		return
	}

	if s.pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "message": "服务未就绪"})
		return
	}
	worker := s.pool.GetWorker(deviceID)
	if worker == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "设备未找到或未运行"})
		return
	}
	nc := worker.NetworkController()
	if nc == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "当前设备不支持网络控制"})
		return
	}
	// enabled=false：落库 network_enabled=false
	patch, ok := s.patchCardPolicyForDeviceTxOrRespond(c, deviceID, "SIM 身份未确认，请等待切卡完成后再关闭蜂窝数据", func(p *db.CardPolicy) {
		p.NetworkEnabled = false
	})
	if !ok {
		return
	}
	if err := worker.StopNetwork(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "停止数据网络失败: " + err.Error() + rollbackCardPolicyPatchMessage(patch)})
		return
	}
	go func() { _ = worker.RefreshRuntime(nil, "stop_network") }()
	c.JSON(http.StatusOK, gin.H{
		"status":            "ok",
		"message":           "数据网络已停止",
		"device":            deviceID,
		"network_connected": worker.NetworkConnected(),
		"private_ip":        "",
		"private_ipv6":      "",
		"public_ip":         "",
		"public_ipv6":       "",
	})
}

func (s *Server) handleDeviceVoWiFiPatch(c *gin.Context) {
	var req enabledPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "enabled 为必填项"})
		return
	}

	deviceID := deviceIDParam(c)

	if *req.Enabled {
		// 落库：仅置 vowifi_enabled=true。不碰 airplane_enabled——它是用户的纯飞行
		// 意图，作为关闭 VoWiFi 后的回退依据；VoWiFi 接管射频由运行时投影派生。
		patch, ok := s.patchCardPolicyForDeviceTxOrRespond(c, deviceID, "SIM 身份未确认，请等待切卡完成后再切换 VoWiFi", vowifiEnablePolicyMutation)
		if !ok {
			return
		}
		// 同步 w.Config，使概览即时切到 VoWiFi 模式面板（EnableVoWiFi 不碰 Config）。
		s.pool.SetWorkerVoWiFiPolicy(deviceID, true)
		if err := s.pool.EnableVoWiFi(deviceID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"status":  "error",
				"message": "VoWiFi 启用失败: " + err.Error() + rollbackCardPolicyPatchMessage(patch),
				"device":  deviceID,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"message": "VoWiFi 已启用，设备已进入飞行模式",
			"device":  deviceID,
		})
		return
	}

	// 落库：仅清 vowifi_enabled=false，保留 airplane_enabled（用户飞行意图）。
	// 关闭 VoWiFi 后 DisableVoWiFi 会按当前卡策略重投影：之前是飞行则回飞行，否则回在线。
	patch, ok := s.patchCardPolicyForDeviceTxOrRespond(c, deviceID, "SIM 身份未确认，请等待切卡完成后再切换 VoWiFi", vowifiDisablePolicyMutation)
	if !ok {
		return
	}
	s.pool.SetWorkerVoWiFiPolicy(deviceID, false)
	if err := s.pool.DisableVoWiFi(deviceID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "VoWiFi 禁用失败: " + err.Error() + rollbackCardPolicyPatchMessage(patch),
			"device":  deviceID,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "VoWiFi 已禁用",
		"device":  deviceID,
	})
}

// vowifiEnablePolicyMutation 开 VoWiFi 的落库副作用：只置 vowifi，飞行意图保持不变。
func vowifiEnablePolicyMutation(p *db.CardPolicy) { p.VoWiFiEnabled = true }

// vowifiDisablePolicyMutation 关 VoWiFi 的落库副作用：只清 vowifi，保留用户飞行意图以便回退。
func vowifiDisablePolicyMutation(p *db.CardPolicy) { p.VoWiFiEnabled = false }

func (s *Server) handleDeviceRoamingPatch(c *gin.Context) {
	var req enabledPatchRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "enabled 为必填项"})
		return
	}

	deviceID := deviceIDParam(c)
	if s.pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "error", "message": "服务未就绪"})
		return
	}
	worker := s.pool.GetWorker(deviceID)
	if worker == nil {
		c.JSON(http.StatusNotFound, gin.H{"status": "error", "message": "设备未找到或未运行"})
		return
	}
	if s.pool.IsESIMSwitching(deviceID) || worker.SIMIdentityUnconfirmed() {
		c.JSON(http.StatusConflict, gin.H{"status": "error", "message": "SIM 身份未确认，请稍后再切换数据漫游策略"})
		return
	}

	patch, ok := s.patchCardPolicyForDeviceTxOrRespond(c, deviceID, "SIM 身份未确认，请稍后再切换数据漫游策略", func(p *db.CardPolicy) {
		p.RoamingEnabled = *req.Enabled
	})
	if !ok {
		return
	}
	if patch.ICCID != "" {
		s.pool.SetWorkerRoamingPolicy(deviceID, *req.Enabled)
	}
	if !*req.Enabled {
		if err := worker.StopNetworkIfDataRoamingDisallowed(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "数据漫游策略已保存，但停止数据网络失败: " + err.Error() + rollbackCardPolicyPatchMessage(patch)})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status":          "ok",
		"roaming_enabled": *req.Enabled,
		"message":         "数据漫游策略已更新",
	})
}
