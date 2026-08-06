package db

import "testing"

func TestPendingPhoneNumbersTableExists(t *testing.T) {
	initPhoneNumberTestDB(t)
	if !DB.Migrator().HasTable(&PendingPhoneNumber{}) {
		t.Fatal("pending_phone_numbers table must exist after Init")
	}
}

func TestRecordVoWiFiPhoneStagesByICCIDWhenIMSIEmpty(t *testing.T) {
	initPhoneNumberTestDB(t)
	iccid := "8944000000000000001"
	if err := RecordVoWiFiPhoneNumber("", iccid, "+447700900123"); err != nil {
		t.Fatalf("RecordVoWiFiPhoneNumber error=%v", err)
	}
	var pending PendingPhoneNumber
	if err := DB.Where("iccid = ?", iccid).First(&pending).Error; err != nil {
		t.Fatalf("First(pending) error=%v", err)
	}
	if pending.PhoneNumber != "+447700900123" || pending.VowifiPhoneNumber != "+447700900123" {
		t.Fatalf("pending=%+v, want phone/vowifi=+447700900123", pending)
	}
}

func TestRecordModemPhoneWritesSubscriptionWhenIMSIKnown(t *testing.T) {
	initPhoneNumberTestDB(t)
	imsi := "234150000000001"
	if err := RecordModemPhoneNumber(imsi, "8944000000000000002", "+447700900124"); err != nil {
		t.Fatalf("RecordModemPhoneNumber error=%v", err)
	}
	got, err := GetSIMCardPhoneNumberByIMSI(imsi)
	if err != nil {
		t.Fatalf("GetSIMCardPhoneNumberByIMSI error=%v", err)
	}
	if got != "+447700900124" {
		t.Fatalf("phone=%q, want +447700900124", got)
	}
	var cnt int64
	DB.Model(&PendingPhoneNumber{}).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("pending rows=%d, want 0 when IMSI known", cnt)
	}
}

func TestPendingPhoneMigratesWhenIMSIResolved(t *testing.T) {
	initPhoneNumberTestDB(t)
	iccid := "8944000000000000003"
	imsi := "234150000000003"

	if err := RecordVoWiFiPhoneNumber("", iccid, "+447700900125"); err != nil {
		t.Fatalf("RecordVoWiFiPhoneNumber error=%v", err)
	}
	imei := "860000000000001"
	if err := UpsertSIMCard(iccid, imsi, "", "TestOp", &imei); err != nil {
		t.Fatalf("UpsertSIMCard error=%v", err)
	}
	got, err := GetSIMCardPhoneNumberByIMSI(imsi)
	if err != nil {
		t.Fatalf("GetSIMCardPhoneNumberByIMSI error=%v", err)
	}
	if got != "+447700900125" {
		t.Fatalf("phone=%q, want +447700900125 after migration", got)
	}
	var cnt int64
	DB.Model(&PendingPhoneNumber{}).Where("iccid = ?", iccid).Count(&cnt)
	if cnt != 0 {
		t.Fatalf("pending rows=%d, want 0 after migration", cnt)
	}
}

func TestPendingMigrationDropsIMSIEqualValue(t *testing.T) {
	initPhoneNumberTestDB(t)
	iccid := "8944000000000000004"
	imsi := "234150000000004"
	if err := RecordVoWiFiPhoneNumber("", iccid, "+"+imsi); err != nil {
		t.Fatalf("RecordVoWiFiPhoneNumber error=%v", err)
	}
	imei := "860000000000002"
	if err := UpsertSIMCard(iccid, imsi, "", "TestOp", &imei); err != nil {
		t.Fatalf("UpsertSIMCard error=%v", err)
	}
	got, _ := GetSIMCardPhoneNumberByIMSI(imsi)
	if got != "" {
		t.Fatalf("phone=%q, want empty (IMSI-equal staged value must not migrate)", got)
	}
}

func TestGetPhoneNumberFallsBackToICCIDStaging(t *testing.T) {
	initPhoneNumberTestDB(t)
	iccid := "8944000000000000005"
	if err := RecordVoWiFiPhoneNumber("", iccid, "+447700900126"); err != nil {
		t.Fatalf("RecordVoWiFiPhoneNumber error=%v", err)
	}
	got, err := GetPhoneNumberByIMSIOrICCID("", iccid)
	if err != nil {
		t.Fatalf("GetPhoneNumberByIMSIOrICCID error=%v", err)
	}
	if got != "+447700900126" {
		t.Fatalf("phone=%q, want +447700900126 from ICCID staging", got)
	}
}

func TestGetPhoneNumberPrefersIMSISubscription(t *testing.T) {
	initPhoneNumberTestDB(t)
	imsi := "234150000000006"
	iccid := "8944000000000000006"
	if err := UpdateSIMCardVoWiFiPhoneNumberByIMSI(imsi, "+447700900127"); err != nil {
		t.Fatalf("UpdateSIMCardVoWiFiPhoneNumberByIMSI error=%v", err)
	}
	got, err := GetPhoneNumberByIMSIOrICCID(imsi, iccid)
	if err != nil {
		t.Fatalf("GetPhoneNumberByIMSIOrICCID error=%v", err)
	}
	if got != "+447700900127" {
		t.Fatalf("phone=%q, want +447700900127 from IMSI subscription", got)
	}
}

func TestManualPhoneStagesByICCIDAndMigratesWithPriority(t *testing.T) {
	initPhoneNumberTestDB(t)
	iccid := "8944000000000000007"
	imsi := "234150000000007"

	if err := RecordModemPhoneNumber("", iccid, "+447700900128"); err != nil {
		t.Fatalf("RecordModemPhoneNumber error=%v", err)
	}
	if err := RecordManualPhoneNumber("", iccid, "+447700900129"); err != nil {
		t.Fatalf("RecordManualPhoneNumber error=%v", err)
	}
	got, err := GetPhoneNumberByIMSIOrICCID("", iccid)
	if err != nil {
		t.Fatalf("GetPhoneNumberByIMSIOrICCID error=%v", err)
	}
	if got != "+447700900129" {
		t.Fatalf("phone=%q, want manual staged phone", got)
	}

	imei := "860000000000007"
	if err := UpsertSIMCard(iccid, imsi, "", "TestOp", &imei); err != nil {
		t.Fatalf("UpsertSIMCard error=%v", err)
	}
	sub := loadSIMSubscriptionByIMSI(t, imsi)
	if sub.ManualPhoneNumber != "+447700900129" || sub.PhoneNumber != "+447700900129" {
		t.Fatalf("subscription=%+v, want manual phone migrated with priority", sub)
	}
	if sub.ModemPhoneNumber != "+447700900128" {
		t.Fatalf("ModemPhoneNumber=%q want staged modem value", sub.ModemPhoneNumber)
	}
}
