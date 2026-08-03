package db

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CardPolicy 是跟随卡(ICCID)走的可配置策略。SMS 恒开、SMSC 动态取，均不在此。
type CardPolicy struct {
	ICCID           string    `gorm:"column:iccid;primaryKey" json:"iccid"`
	NetworkEnabled  bool      `gorm:"column:network_enabled" json:"network_enabled"`
	VoWiFiEnabled   bool      `gorm:"column:vowifi_enabled" json:"vowifi_enabled"`
	AirplaneEnabled bool      `gorm:"column:airplane_enabled" json:"airplane_enabled"`
	RoamingEnabled  bool      `gorm:"column:roaming_enabled" json:"roaming_enabled"`
	IPVersion       string    `gorm:"column:ip_version" json:"ip_version"`
	APN             string    `gorm:"column:apn" json:"apn"`
	Source          string    `gorm:"column:source" json:"source"` // auto | user
	CreatedAt       time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (CardPolicy) TableName() string { return "card_policies" }

// CanonicalICCID 规整 ICCID 为唯一形态：trim 空白、去引号、去尾部 BCD 填充位 F/f。
// 必须用于 card_policies 的所有读写边界——否则 eSIM profile 侧（BCD 解码已剥 F）与
// 运行时身份侧（QMI DMS 原样保留 F）会落成两行不同的策略，UI 读到的对不上。
func CanonicalICCID(iccid string) string {
	v := strings.TrimSpace(iccid)
	v = strings.Trim(v, "\"")
	v = strings.TrimRight(v, "Ff")
	return v
}

// DefaultCardPolicy 是新卡自动建档用的硬编码安全默认（不落配置文件）。
func DefaultCardPolicy(iccid string) CardPolicy {
	return CardPolicy{
		ICCID:           CanonicalICCID(iccid),
		NetworkEnabled:  false,
		VoWiFiEnabled:   false,
		AirplaneEnabled: false,
		RoamingEnabled:  true,
		IPVersion:       "v4",
		APN:             "",
		Source:          "auto",
	}
}

// NormalizeCardPolicy 仅做字段归一（trim ICCID、空 ip 归一为 v4）。
// 注意：airplane_enabled 表示“用户的纯飞行意图”，独立于 vowifi——不再强制
// vowifi=on ⇒ airplane=on。VoWiFi 接管射频是运行时投影的派生行为（见
// applyPolicyToWorker / resolveAndApplyPolicy 的 VoWiFi 优先分支），不污染存储意图；
// 这样关闭 VoWiFi 后能按存储的飞行意图正确回退（之前是飞行回飞行，之前在线回在线）。
func NormalizeCardPolicy(p *CardPolicy) {
	if p == nil {
		return
	}
	p.ICCID = CanonicalICCID(p.ICCID)
	switch strings.TrimSpace(p.IPVersion) {
	case "v4", "v6", "v4v6":
		p.IPVersion = strings.TrimSpace(p.IPVersion)
	default:
		p.IPVersion = "v4"
	}
}

// ErrCardPolicyNotFound 表示 DB 中没有该 ICCID 的策略行。
var ErrCardPolicyNotFound = errors.New("card policy not found")

// GetCardPolicy 读一行；缺失返回 ErrCardPolicyNotFound。
func GetCardPolicy(iccid string) (CardPolicy, error) {
	iccid = CanonicalICCID(iccid)
	if iccid == "" || DB == nil {
		return CardPolicy{}, ErrCardPolicyNotFound
	}
	var out CardPolicy
	err := DB.Where("iccid = ?", iccid).First(&out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return CardPolicy{}, ErrCardPolicyNotFound
	}
	return out, err
}

// UpsertCardPolicy 写入/覆盖一行（归一化不变式后落库）。
func UpsertCardPolicy(p CardPolicy) error {
	if DB == nil {
		return errors.New("db 未初始化")
	}
	NormalizeCardPolicy(&p)
	if p.ICCID == "" {
		return errors.New("ICCID 为空")
	}
	now := time.Now()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "iccid"}},
		DoUpdates: clause.Assignments(map[string]any{
			"network_enabled":  p.NetworkEnabled,
			"vowifi_enabled":   p.VoWiFiEnabled,
			"airplane_enabled": p.AirplaneEnabled,
			"roaming_enabled":  p.RoamingEnabled,
			"ip_version":       p.IPVersion,
			"apn":              p.APN,
			"source":           p.Source,
			"updated_at":       p.UpdatedAt,
		}),
	}).Create(&p).Error
}

// ResolveCardPolicy 解析 ICCID→策略；缺失则按默认模板自动建档并返回。
func ResolveCardPolicy(iccid string) (CardPolicy, error) {
	got, err := GetCardPolicy(iccid)
	if err == nil {
		return got, nil
	}
	if !errors.Is(err, ErrCardPolicyNotFound) {
		return CardPolicy{}, err
	}
	def := DefaultCardPolicy(iccid)
	if def.ICCID == "" {
		return CardPolicy{}, ErrCardPolicyNotFound
	}
	if err := UpsertCardPolicy(def); err != nil {
		return CardPolicy{}, err
	}
	return GetCardPolicy(iccid)
}
