package db

import (
	"errors"
	"strings"
	"time"

	"github.com/swordstudiox/vohive-plus/internal/config"

	"gorm.io/gorm"
)

type ProxyInstance struct {
	ID          string    `gorm:"primaryKey" json:"id"`
	Name        string    `json:"name"`
	DeviceID    string    `gorm:"index" json:"device_id"`
	Enabled     bool      `json:"enabled"`
	Mode        string    `json:"mode"`
	ListenAddr  string    `json:"listen_addr"`
	ListenPort  int       `json:"listen_port"`
	AuthEnabled bool      `json:"auth_enabled"`
	Username    string    `json:"username"`
	Password    string    `json:"password,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func ProxyInstanceFromConfig(in config.ProxyInstance) (ProxyInstance, error) {
	out := ProxyInstance{
		ID:          strings.TrimSpace(in.ID),
		Name:        strings.TrimSpace(in.Name),
		DeviceID:    strings.TrimSpace(in.DeviceID),
		Enabled:     in.Enabled,
		Mode:        normalizeProxyModeValue(in.Mode),
		ListenAddr:  strings.TrimSpace(in.ListenAddr),
		ListenPort:  in.ListenPort,
		AuthEnabled: in.AuthEnabled,
		Username:    strings.TrimSpace(in.Username),
		Password:    strings.TrimSpace(in.Password),
	}
	if out.ID == "" {
		return ProxyInstance{}, errors.New("empty id")
	}
	if !out.AuthEnabled {
		out.Username = ""
		out.Password = ""
	}
	return out, nil
}

func (p ProxyInstance) ToConfig() (config.ProxyInstance, error) {
	out := config.ProxyInstance{
		ID:          strings.TrimSpace(p.ID),
		Name:        strings.TrimSpace(p.Name),
		DeviceID:    strings.TrimSpace(p.DeviceID),
		Enabled:     p.Enabled,
		Mode:        normalizeProxyModeValue(p.Mode),
		ListenAddr:  strings.TrimSpace(p.ListenAddr),
		ListenPort:  p.ListenPort,
		AuthEnabled: p.AuthEnabled,
		Username:    strings.TrimSpace(p.Username),
		Password:    p.Password,
	}
	if !out.AuthEnabled {
		out.Username = ""
		out.Password = ""
	}
	return out, nil
}

func CountProxyInstances() (int64, error) {
	var n int64
	if err := DB.Model(&ProxyInstance{}).Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

func ListProxyInstances() ([]ProxyInstance, error) {
	var out []ProxyInstance
	if err := DB.Order("id asc").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func GetProxyInstanceByID(id string) (*ProxyInstance, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("empty id")
	}
	var out ProxyInstance
	err := DB.First(&out, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func normalizeProxyModeValue(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "http":
		return "http"
	case "socks", "socks5", "":
		return "socks5"
	default:
		return "socks5"
	}
}
