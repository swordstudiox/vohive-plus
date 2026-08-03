package db

import (
	"testing"
)

func TestCardPolicyTableMigrated(t *testing.T) {
	openTestDB(t)
	if !DB.Migrator().HasTable(&CardPolicy{}) {
		t.Fatal("card_policies 表未建")
	}
	for _, col := range []string{"iccid", "network_enabled", "vowifi_enabled", "airplane_enabled", "roaming_enabled", "ip_version", "apn", "source"} {
		if !DB.Migrator().HasColumn(&CardPolicy{}, col) {
			t.Fatalf("card_policies 缺列 %s", col)
		}
	}
}

func TestDefaultCardPolicy(t *testing.T) {
	p := DefaultCardPolicy("8986001234567890123")
	if p.ICCID != "8986001234567890123" {
		t.Fatalf("ICCID=%q", p.ICCID)
	}
	if p.NetworkEnabled || p.VoWiFiEnabled || p.AirplaneEnabled {
		t.Fatal("默认应全关")
	}
	if !p.RoamingEnabled {
		t.Fatal("默认应允许漫游，避免升级后静默禁止漫游")
	}
	if p.IPVersion != "v4" {
		t.Fatalf("默认 ip=%q，应 v4", p.IPVersion)
	}
	if p.Source != "auto" {
		t.Fatalf("source=%q，应 auto", p.Source)
	}
}

// airplane_enabled 是独立的“用户飞行意图”，归一不再因 vowifi=on 强制 airplane=on，
// 否则关闭 VoWiFi 后无法区分“飞行是用户主动开的”还是“VoWiFi 的副产品”，导致无法回退。
func TestNormalizeCardPolicyAirplaneIndependent(t *testing.T) {
	// vowifi=on 但用户未开飞行：airplane 保持 false（之前是在线，关 vowifi 应回在线）
	p := CardPolicy{ICCID: "x", VoWiFiEnabled: true, AirplaneEnabled: false, IPVersion: ""}
	NormalizeCardPolicy(&p)
	if p.AirplaneEnabled {
		t.Fatal("vowifi=on 不应强制 airplane=on：airplane 须保持用户意图")
	}
	if p.IPVersion != "v4" {
		t.Fatalf("空 ip 应归一为 v4，得 %q", p.IPVersion)
	}

	// vowifi=on 且用户开了飞行：airplane 保持 true（之前是飞行，关 vowifi 应回飞行）
	q := CardPolicy{ICCID: "x", VoWiFiEnabled: true, AirplaneEnabled: true}
	NormalizeCardPolicy(&q)
	if !q.AirplaneEnabled {
		t.Fatal("用户已开飞行意图须保留")
	}
}

func TestResolveCardPolicyAutoCreates(t *testing.T) {
	openTestDB(t)
	iccid := "8986009999999999999"

	p, err := ResolveCardPolicy(iccid)
	if err != nil {
		t.Fatalf("ResolveCardPolicy error=%v", err)
	}
	if p.Source != "auto" || p.IPVersion != "v4" {
		t.Fatalf("自动建档默认不符: %+v", p)
	}

	var count int64
	DB.Model(&CardPolicy{}).Where("iccid = ?", iccid).Count(&count)
	if count != 1 {
		t.Fatalf("应已落库一行，得 %d", count)
	}
}

func TestUpsertCardPolicyUserOverride(t *testing.T) {
	openTestDB(t)
	iccid := "8986001111111111111"
	if _, err := ResolveCardPolicy(iccid); err != nil { // 先自动建档
		t.Fatal(err)
	}

	in := CardPolicy{ICCID: iccid, NetworkEnabled: true, VoWiFiEnabled: true, AirplaneEnabled: true, RoamingEnabled: false, IPVersion: "v4v6", Source: "user"}
	if err := UpsertCardPolicy(in); err != nil {
		t.Fatalf("Upsert error=%v", err)
	}

	got, err := GetCardPolicy(iccid)
	if err != nil {
		t.Fatal(err)
	}
	if !got.NetworkEnabled || !got.VoWiFiEnabled || !got.AirplaneEnabled {
		t.Fatalf("写入的字段应原样落库（airplane 独立存储）: %+v", got)
	}
	if got.RoamingEnabled {
		t.Fatalf("roaming_enabled=false 应可被用户策略覆盖: %+v", got)
	}
	if got.IPVersion != "v4v6" || got.Source != "user" {
		t.Fatalf("覆盖未生效: %+v", got)
	}
}

func TestGetCardPolicyMissing(t *testing.T) {
	openTestDB(t)
	if _, err := GetCardPolicy("nope"); err == nil {
		t.Fatal("缺失应报错")
	}
}
