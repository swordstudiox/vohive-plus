package db

import (
	"errors"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/upstreamproxy"
	"gorm.io/gorm"
)

// UpstreamProxy 前置代理实例（用于代理 VoWiFi 的 ePDG 连接）
// 通过 Socks5 UDP Associate 将 IKE/ESP 流量转发到 ePDG
type UpstreamProxy struct {
	ID        string    `gorm:"primaryKey" json:"id"`
	Name      string    `json:"name"`
	Addr      string    `json:"addr"`               // Socks5 服务器地址 (host:port)
	Username  string    `json:"username"`           // 可选鉴权用户名
	Password  string    `json:"password,omitempty"` // 可选鉴权密码
	Enabled   bool      `json:"enabled"`            // 是否启用
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UpstreamProxyCountryRule 将 SIM home country 路由到指定前置代理。
type UpstreamProxyCountryRule struct {
	CountryCode     string    `gorm:"primaryKey" json:"country_code"`
	UpstreamProxyID string    `gorm:"index" json:"upstream_proxy_id"`
	Enabled         bool      `json:"enabled"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (UpstreamProxyCountryRule) TableName() string {
	return "upstream_proxy_country_rules"
}

// ── UpstreamProxy CRUD ──

// ListUpstreamProxies 列出所有前置代理实例
func ListUpstreamProxies() ([]UpstreamProxy, error) {
	var out []UpstreamProxy
	if err := DB.Order("id asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// GetUpstreamProxyByID 根据 ID 获取前置代理
func GetUpstreamProxyByID(id string) (*UpstreamProxy, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("empty id")
	}
	var out UpstreamProxy
	err := DB.First(&out, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

// UpsertUpstreamProxy 创建或更新前置代理
func UpsertUpstreamProxy(p UpstreamProxy) error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("empty id")
	}
	if strings.TrimSpace(p.Addr) == "" {
		return errors.New("empty addr")
	}
	return DB.Save(&p).Error
}

// DeleteUpstreamProxy 删除前置代理（同时清理关联的国家规则）
func DeleteUpstreamProxy(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("empty id")
	}
	if err := DB.Delete(&UpstreamProxyCountryRule{}, "upstream_proxy_id = ?", id).Error; err != nil {
		return err
	}
	return DB.Delete(&UpstreamProxy{}, "id = ?", id).Error
}

// ── UpstreamProxyCountryRule 国家规则管理 ──

func ListUpstreamProxyCountryRules() ([]UpstreamProxyCountryRule, error) {
	var out []UpstreamProxyCountryRule
	if err := DB.Order("country_code asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func UpsertUpstreamProxyCountryRule(rule UpstreamProxyCountryRule) error {
	rule.CountryCode = upstreamproxy.NormalizeCountryCode(rule.CountryCode)
	rule.UpstreamProxyID = strings.TrimSpace(rule.UpstreamProxyID)
	if rule.CountryCode == "" {
		return errors.New("empty country_code")
	}
	if rule.UpstreamProxyID == "" {
		return errors.New("empty upstream_proxy_id")
	}
	rule.UpdatedAt = time.Now()
	return DB.Save(&rule).Error
}

func DeleteUpstreamProxyCountryRule(countryCode string) error {
	countryCode = upstreamproxy.NormalizeCountryCode(countryCode)
	if countryCode == "" {
		return errors.New("empty country_code")
	}
	return DB.Delete(&UpstreamProxyCountryRule{}, "country_code = ?", countryCode).Error
}

func GetCountryUpstreamProxy(countryCode string) (*UpstreamProxy, error) {
	countryCode = upstreamproxy.NormalizeCountryCode(countryCode)
	if countryCode == "" {
		return nil, nil
	}
	var rule UpstreamProxyCountryRule
	err := DB.First(&rule, "country_code = ?", countryCode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !rule.Enabled || strings.TrimSpace(rule.UpstreamProxyID) == "" {
		return nil, nil
	}
	proxy, err := GetUpstreamProxyByID(rule.UpstreamProxyID)
	if err != nil || proxy == nil || !proxy.Enabled {
		return nil, err
	}
	return proxy, nil
}

func GetHomeMCCUpstreamProxy(homeMCC string) (*UpstreamProxy, string, error) {
	countryCode, ok := upstreamproxy.CountryCodeFromHomeMCC(homeMCC)
	if !ok {
		return nil, "", nil
	}
	proxy, err := GetCountryUpstreamProxy(countryCode)
	return proxy, countryCode, err
}
