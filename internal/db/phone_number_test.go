package db

import (
	"path/filepath"
	"testing"
)

func initPhoneNumberTestDB(t *testing.T) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "phone_number.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	t.Cleanup(func() { DB = nil })
}

func loadSIMCardByIMSI(t *testing.T, imsi string) SIMCard {
	t.Helper()
	var sim SIMCard
	if err := DB.Where("imsi = ?", imsi).First(&sim).Error; err != nil {
		t.Fatalf("First(sim) error=%v", err)
	}
	return sim
}

func loadSIMSubscriptionByIMSI(t *testing.T, imsi string) SIMSubscription {
	t.Helper()
	var sub SIMSubscription
	if err := DB.Where("imsi = ?", imsi).First(&sub).Error; err != nil {
		t.Fatalf("First(subscription) error=%v", err)
	}
	return sub
}

func countSIMCardsByICCID(t *testing.T, iccid string) int64 {
	t.Helper()
	var count int64
	if err := DB.Model(&SIMCard{}).Where("iccid = ?", iccid).Count(&count).Error; err != nil {
		t.Fatalf("Count(sim_cards) error=%v", err)
	}
	return count
}

func simCardColumnExists(t *testing.T, column string) bool {
	t.Helper()
	var rows []struct {
		Name string `gorm:"column:name"`
	}
	if err := DB.Raw("PRAGMA table_info(sim_cards)").Scan(&rows).Error; err != nil {
		t.Fatalf("PRAGMA table_info(sim_cards) error=%v", err)
	}
	for _, row := range rows {
		if row.Name == column {
			return true
		}
	}
	return false
}

func closePhoneNumberTestDB(t *testing.T) {
	t.Helper()
	if DB == nil {
		return
	}
	if sqlDB, err := DB.DB(); err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
	DB = nil
}

func TestUpdateSIMCardPhoneNumberByIMSICreatesSubscriptionOnly(t *testing.T) {
	initPhoneNumberTestDB(t)

	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-reader-1", "tel:+8613900139000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}

	sub := loadSIMSubscriptionByIMSI(t, "imsi-reader-1")
	if sub.PhoneNumber != "+8613900139000" {
		t.Fatalf("subscription PhoneNumber=%q want +8613900139000", sub.PhoneNumber)
	}
	if sub.VowifiPhoneNumber != "+8613900139000" {
		t.Fatalf("subscription VowifiPhoneNumber=%q want +8613900139000", sub.VowifiPhoneNumber)
	}
	if got := countSIMCardsByICCID(t, "reader-imsi-imsi-reader-1"); got != 0 {
		t.Fatalf("synthetic sim_cards row count=%d want 0", got)
	}
}

func TestSIMSubscriptionPhoneSurvivesReInitWithRealSIMCardRow(t *testing.T) {
	old := DB
	dbPath := filepath.Join(t.TempDir(), "sim_subscription_reinit.db")
	if err := Init(dbPath); err != nil {
		t.Fatalf("Init() error=%v", err)
	}
	t.Cleanup(func() {
		closePhoneNumberTestDB(t)
		DB = old
	})

	imei := "imei-reinit"
	if err := UpsertSIMCardIdentity("8986000000000000123", "001010000000123", "中国联通", &imei); err != nil {
		t.Fatalf("UpsertSIMCardIdentity() error=%v", err)
	}
	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("001010000000123", "+8613900139123"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}

	closePhoneNumberTestDB(t)
	if err := Init(dbPath); err != nil {
		t.Fatalf("re-Init() error=%v", err)
	}

	sub := loadSIMSubscriptionByIMSI(t, "001010000000123")
	if sub.CurrentICCID != "8986000000000000123" {
		t.Fatalf("CurrentICCID=%q want real ICCID", sub.CurrentICCID)
	}
	if sub.PhoneNumber != "+8613900139123" || sub.VowifiPhoneNumber != "+8613900139123" {
		t.Fatalf("subscription phone fields lost after re-init: %+v", sub)
	}
}

func TestSIMCardsSchemaNoLongerStoresPhoneFields(t *testing.T) {
	initPhoneNumberTestDB(t)

	for _, column := range []string{"phone_number", "modem_phone_number", "vowifi_phone_number"} {
		if simCardColumnExists(t, column) {
			t.Fatalf("sim_cards should not have phone column %q", column)
		}
	}
	if !DB.Migrator().HasColumn(&SIMSubscription{}, "phone_number") {
		t.Fatal("sim_subscriptions should have phone_number column")
	}
}

func TestUpdateSIMCardModemPhoneNumberByIMSIStoresFinalPhoneNumber(t *testing.T) {
	initPhoneNumberTestDB(t)

	if err := UpdateSIMCardModemPhoneNumberByIMSI("imsi-1", " +8613800138000 "); err != nil {
		t.Fatalf("UpdateSIMCardModemPhoneNumberByIMSI() error=%v", err)
	}

	sub := loadSIMSubscriptionByIMSI(t, "imsi-1")
	if sub.ModemPhoneNumber != "+8613800138000" {
		t.Fatalf("ModemPhoneNumber=%q want=+8613800138000", sub.ModemPhoneNumber)
	}
	if sub.VowifiPhoneNumber != "" {
		t.Fatalf("VowifiPhoneNumber=%q want empty", sub.VowifiPhoneNumber)
	}
	if sub.PhoneNumber != "+8613800138000" {
		t.Fatalf("PhoneNumber=%q want=+8613800138000", sub.PhoneNumber)
	}

	phone, err := GetSIMCardPhoneNumberByIMSI("imsi-1")
	if err != nil {
		t.Fatalf("GetSIMCardPhoneNumberByIMSI() error=%v", err)
	}
	if phone != "+8613800138000" {
		t.Fatalf("GetSIMCardPhoneNumberByIMSI()=%q want=+8613800138000", phone)
	}
}

func TestUpdateSIMCardVoWiFiPhoneNumberByIMSIStoresFinalPhoneNumber(t *testing.T) {
	initPhoneNumberTestDB(t)

	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-2", "tel:+8613900139000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}

	sub := loadSIMSubscriptionByIMSI(t, "imsi-2")
	if sub.ModemPhoneNumber != "" {
		t.Fatalf("ModemPhoneNumber=%q want empty", sub.ModemPhoneNumber)
	}
	if sub.VowifiPhoneNumber != "+8613900139000" {
		t.Fatalf("VowifiPhoneNumber=%q want=+8613900139000", sub.VowifiPhoneNumber)
	}
	if sub.PhoneNumber != "+8613900139000" {
		t.Fatalf("PhoneNumber=%q want=+8613900139000", sub.PhoneNumber)
	}
}

func TestUpdateSIMCardVoWiFiPhoneNumberByIMSIPrefersVoWiFiOverModem(t *testing.T) {
	initPhoneNumberTestDB(t)

	if err := UpdateSIMCardModemPhoneNumberByIMSI("imsi-3", "+8613700137000"); err != nil {
		t.Fatalf("UpdateSIMCardModemPhoneNumberByIMSI() error=%v", err)
	}
	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-3", "+8613600136000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}

	sub := loadSIMSubscriptionByIMSI(t, "imsi-3")
	if sub.ModemPhoneNumber != "+8613700137000" {
		t.Fatalf("ModemPhoneNumber=%q want=+8613700137000", sub.ModemPhoneNumber)
	}
	if sub.VowifiPhoneNumber != "+8613600136000" {
		t.Fatalf("VowifiPhoneNumber=%q want=+8613600136000", sub.VowifiPhoneNumber)
	}
	if sub.PhoneNumber != "+8613600136000" {
		t.Fatalf("PhoneNumber=%q want=+8613600136000", sub.PhoneNumber)
	}
}

func TestManualPhoneNumberHasHighestPriority(t *testing.T) {
	initPhoneNumberTestDB(t)

	if err := UpdateSIMCardModemPhoneNumberByIMSI("imsi-manual-1", "+8613700137000"); err != nil {
		t.Fatalf("UpdateSIMCardModemPhoneNumberByIMSI() error=%v", err)
	}
	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-manual-1", "+8613600136000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}
	if err := SetSIMCardManualPhoneNumberByIMSI("imsi-manual-1", "+8613500135000"); err != nil {
		t.Fatalf("SetSIMCardManualPhoneNumberByIMSI() error=%v", err)
	}
	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-manual-1", "+8613400134000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI(second) error=%v", err)
	}
	if err := UpdateSIMCardModemPhoneNumberByIMSI("imsi-manual-1", "+8613300133000"); err != nil {
		t.Fatalf("UpdateSIMCardModemPhoneNumberByIMSI(second) error=%v", err)
	}

	sub := loadSIMSubscriptionByIMSI(t, "imsi-manual-1")
	if sub.ManualPhoneNumber != "+8613500135000" {
		t.Fatalf("ManualPhoneNumber=%q want=+8613500135000", sub.ManualPhoneNumber)
	}
	if sub.PhoneNumber != "+8613500135000" {
		t.Fatalf("PhoneNumber=%q want manual value", sub.PhoneNumber)
	}

	if err := SetSIMCardManualPhoneNumberByIMSI("imsi-manual-1", ""); err != nil {
		t.Fatalf("SetSIMCardManualPhoneNumberByIMSI(clear) error=%v", err)
	}
	sub = loadSIMSubscriptionByIMSI(t, "imsi-manual-1")
	if sub.ManualPhoneNumber != "" {
		t.Fatalf("ManualPhoneNumber=%q want empty after clear", sub.ManualPhoneNumber)
	}
	if sub.PhoneNumber != "+8613400134000" {
		t.Fatalf("PhoneNumber=%q want latest VoWiFi value after clearing manual", sub.PhoneNumber)
	}
}

func TestInvalidPhoneNumbersDoNotOverwriteExistingValue(t *testing.T) {
	initPhoneNumberTestDB(t)

	if err := UpdateSIMCardModemPhoneNumberByIMSI("imsi-4", "+8613500135000"); err != nil {
		t.Fatalf("UpdateSIMCardModemPhoneNumberByIMSI(valid modem) error=%v", err)
	}
	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-4", "+8613600136000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI(valid vowifi) error=%v", err)
	}

	for _, invalid := range []string{"", "   ", "00000000000", "FFFFFFFF", "ffffffffffff", "Own Number"} {
		if err := UpdateSIMCardModemPhoneNumberByIMSI("imsi-4", invalid); err != nil {
			t.Fatalf("UpdateSIMCardModemPhoneNumberByIMSI(%q) error=%v", invalid, err)
		}
		if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-4", invalid); err != nil {
			t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI(%q) error=%v", invalid, err)
		}
	}

	sub := loadSIMSubscriptionByIMSI(t, "imsi-4")
	if sub.ModemPhoneNumber != "+8613500135000" {
		t.Fatalf("ModemPhoneNumber=%q want=+8613500135000", sub.ModemPhoneNumber)
	}
	if sub.VowifiPhoneNumber != "+8613600136000" {
		t.Fatalf("VowifiPhoneNumber=%q want=+8613600136000", sub.VowifiPhoneNumber)
	}
	if sub.PhoneNumber != "+8613600136000" {
		t.Fatalf("PhoneNumber=%q want=+8613600136000", sub.PhoneNumber)
	}
}

func TestUpsertSIMCardDoesNotClearExistingPhoneFields(t *testing.T) {
	initPhoneNumberTestDB(t)

	if err := UpdateSIMCardModemPhoneNumberByIMSI("imsi-5", "+8613500135000"); err != nil {
		t.Fatalf("UpdateSIMCardModemPhoneNumberByIMSI() error=%v", err)
	}
	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI("imsi-5", "+8613600136000"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI() error=%v", err)
	}
	imei := "imei-5"
	if err := UpsertSIMCard("8986000000000000005", "imsi-5", "", "中国移动", &imei); err != nil {
		t.Fatalf("UpsertSIMCard() error=%v", err)
	}

	sub := loadSIMSubscriptionByIMSI(t, "imsi-5")
	if sub.ModemPhoneNumber != "+8613500135000" {
		t.Fatalf("ModemPhoneNumber=%q want=+8613500135000", sub.ModemPhoneNumber)
	}
	if sub.VowifiPhoneNumber != "+8613600136000" {
		t.Fatalf("VowifiPhoneNumber=%q want=+8613600136000", sub.VowifiPhoneNumber)
	}
	if sub.PhoneNumber != "+8613600136000" {
		t.Fatalf("PhoneNumber=%q want=+8613600136000", sub.PhoneNumber)
	}
	if sub.CurrentICCID != "8986000000000000005" {
		t.Fatalf("CurrentICCID=%q want=8986000000000000005", sub.CurrentICCID)
	}
}
