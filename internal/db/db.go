package db

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

var ErrSMSNotFound = errors.New("sms not found")

// Device 模块设备表 (主键: IMEI)
type Device struct {
	IMEI         string    `gorm:"primaryKey" json:"imei"`
	Alias        string    `json:"alias"`
	Model        string    `json:"model"`
	Firmware     string    `json:"firmware"`
	Port         string    `json:"port"`
	PublicIP     string    `json:"public_ip"`  // 当前公网IP
	PrivateIP    string    `json:"private_ip"` // 当前内网IP
	PublicIPv6   string    `json:"public_ipv6"`
	PrivateIPv6  string    `json:"private_ipv6"`
	CurrentICCID *string   `gorm:"column:iccid" json:"current_iccid"`
	SimInserted  bool      `json:"sim_inserted"`
	SignalDBM    int       `json:"signal_dbm"`
	SignalRSRQ   int       `json:"signal_rsrq"`
	SignalRSRP   int       `json:"signal_rsrp"`
	LastSeen     time.Time `json:"last_seen"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// SIMCard SIM卡表 (主键: ICCID)
type SIMCard struct {
	ICCID         string    `gorm:"column:iccid;primaryKey" json:"iccid"`
	IMSI          string    `json:"imsi"`
	Operator      string    `json:"operator"`        // 运营商
	CurrentIMEI   *string   `json:"current_imei"`    // 当前所在的设备
	RegStatus     int       `json:"reg_status"`      // 网络注册状态 (0-5)
	RegStatusText string    `json:"reg_status_text"` // 注册状态文本
	LAC           string    `json:"lac"`             // 位置区代码
	CellID        string    `json:"cell_id"`         // 小区 ID
	APN           string    `json:"apn"`             // 接入点
	IMSStatus     int       `json:"ims_status"`      // IMS 注册状态
	LastSeen      time.Time `json:"last_seen"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type SIMSubscription struct {
	IMSI              string    `gorm:"column:imsi;primaryKey" json:"imsi"`
	CurrentICCID      string    `gorm:"column:current_iccid;index" json:"current_iccid"`
	PhoneNumber       string    `gorm:"column:phone_number" json:"phone_number"`
	ManualPhoneNumber string    `gorm:"column:manual_phone_number" json:"manual_phone_number"`
	ModemPhoneNumber  string    `gorm:"column:modem_phone_number" json:"modem_phone_number"`
	VowifiPhoneNumber string    `gorm:"column:vowifi_phone_number" json:"vowifi_phone_number"`
	Operator          string    `gorm:"column:operator" json:"operator"`
	LastSeen          time.Time `gorm:"column:last_seen" json:"last_seen"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// PendingPhoneNumber 在 IMSI 未知时按 ICCID 暂存本机号码，IMSI 到位后迁移进 sim_subscriptions。
type PendingPhoneNumber struct {
	ICCID             string    `gorm:"column:iccid;primaryKey" json:"iccid"`
	PhoneNumber       string    `gorm:"column:phone_number" json:"phone_number"`
	ManualPhoneNumber string    `gorm:"column:manual_phone_number" json:"manual_phone_number"`
	ModemPhoneNumber  string    `gorm:"column:modem_phone_number" json:"modem_phone_number"`
	VowifiPhoneNumber string    `gorm:"column:vowifi_phone_number" json:"vowifi_phone_number"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (PendingPhoneNumber) TableName() string { return "pending_phone_numbers" }

// SMS 短信表 (关联 IMSI)
type SMS struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	IMSI       string    `gorm:"column:imsi;index:idx_sms_imsi_peer_ts,priority:1;index:idx_sms_imsi_ts,priority:1" json:"imsi"`
	ICCID      string    `gorm:"column:iccid;index" json:"iccid"`
	Peer       string    `gorm:"column:peer;index:idx_sms_imsi_peer_ts,priority:2" json:"peer"`
	LocalPhone string    `gorm:"column:local_phone;index" json:"local_phone"`
	Sender     string    `json:"sender"`
	Recipient  string    `json:"recipient"`
	Content    string    `json:"content"`
	Type       int       `json:"type"`   // 1: 接收, 2: 发送
	Status     int       `json:"status"` // 0: 未读, 1: 已读, 2: 发送成功, 3: 发送失败
	Timestamp  time.Time `gorm:"index:idx_sms_imsi_peer_ts,priority:3,sort:desc;index:idx_sms_ts,sort:desc;index:idx_sms_imsi_ts,priority:2,sort:desc" json:"timestamp"`
	CreatedAt  time.Time `json:"created_at"`
}

type SMSContact struct {
	IMSI          string    `gorm:"column:imsi;primaryKey;index:idx_sms_contact_imsi_last_ts,priority:1" json:"imsi"`
	ICCID         string    `gorm:"column:iccid;index" json:"iccid"`
	Peer          string    `gorm:"column:peer;primaryKey" json:"peer"`
	LastSMSID     uint      `gorm:"column:last_sms_id" json:"last_sms_id"`
	LastTimestamp time.Time `gorm:"column:last_timestamp;index:idx_sms_contact_imsi_last_ts,priority:2,sort:desc;index:idx_sms_contact_last_ts,sort:desc" json:"last_timestamp"`
	LastContent   string    `gorm:"column:last_content" json:"last_content"`
	LastType      int       `gorm:"column:last_type" json:"last_type"`
	UnreadCount   int       `gorm:"column:unread_count" json:"unread_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Init 初始化数据库连接
func Init(dbPath string) error {
	var err error
	dsn := strings.TrimSpace(dbPath)
	if dsn == "" {
		dsn = dbPath
	}
	driverName := strings.TrimSpace(os.Getenv("VOHIVE_SQLITE_DRIVER"))
	if driverName == "" {
		driverName = "modernc"
	}

	dialector, err := openSQLiteDialector(driverName, dsn)
	if err != nil {
		return err
	}

	DB, err = gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return err
	}

	sqlDB, err := DB.DB()
	if err != nil || sqlDB == nil {
		return fmt.Errorf("open db: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := applySQLitePragmas(DB); err != nil {
		return err
	}

	cardPolicyHadRoamingColumn := DB.Migrator().HasTable(&CardPolicy{}) &&
		DB.Migrator().HasColumn(&CardPolicy{}, "roaming_enabled")

	// 自动迁移
	if err := DB.AutoMigrate(
		&Device{},
		&CardPolicy{},
		&SIMCard{},
		&SIMSubscription{},
		&PendingPhoneNumber{},
		&ProxyInstance{},
		&UpstreamProxy{},
		&UpstreamProxyCountryRule{},
		&SMS{},
		&SMSContact{},
		&SMSDelivery{},
		&SMSDeliveryPart{},
		&TrafficMinute{},
		&TrafficHour{},
		&TrafficDay{},
		&TrafficWeek{},
		&TrafficMonth{},
	); err != nil {
		return err
	}
	if err := RunICCIDReKeyMigration(DB); err != nil {
		return err
	}
	if err := MigrateCardPolicyCanonicalICCID(DB); err != nil {
		return err
	}
	if !cardPolicyHadRoamingColumn {
		if err := InitializeCardPolicyRoamingDefault(DB); err != nil {
			return err
		}
	}
	return nil
}

func applySQLitePragmas(db *gorm.DB) error {
	stmts := []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// UpsertDevice 更新或插入设备记录
func UpsertDevice(imei, alias, model, port string) error {
	if DB == nil {
		return nil
	}
	var device Device
	result := DB.Where("imei = ?", imei).First(&device)
	if result.Error == gorm.ErrRecordNotFound {
		device = Device{
			IMEI:      imei,
			Alias:     alias,
			Model:     model,
			Port:      port,
			LastSeen:  time.Now(),
			CreatedAt: time.Now(),
		}
		return DB.Create(&device).Error
	}
	// 更新现有设备
	return DB.Model(&device).Updates(map[string]interface{}{
		"alias":     alias,
		"model":     model,
		"port":      port,
		"last_seen": time.Now(),
	}).Error
}

// UpsertSIMCard 更新或插入 SIM 卡记录
func UpsertSIMCard(iccid, imsi, phoneNumber, operator string, currentIMEI *string) error {
	if err := UpsertSIMCardIdentity(iccid, imsi, operator, currentIMEI); err != nil {
		return err
	}
	if strings.TrimSpace(imsi) != "" {
		if err := migratePendingPhoneToSubscription(imsi, iccid); err != nil {
			return err
		}
	}
	if normalized := normalizeSIMPhoneNumber(phoneNumber); normalized != "" {
		return UpdateSIMCardModemPhoneNumberByIMSI(imsi, normalized)
	}
	return nil
}

func UpsertSIMCardIdentity(iccid, imsi, operator string, currentIMEI *string) error {
	if DB == nil {
		return nil
	}
	iccid = strings.TrimSpace(iccid)
	imsi = strings.TrimSpace(imsi)
	operator = strings.TrimSpace(operator)
	if iccid == "" {
		return nil
	}
	now := time.Now()
	sim := SIMCard{
		ICCID:       iccid,
		IMSI:        imsi,
		Operator:    operator,
		CurrentIMEI: currentIMEI,
		LastSeen:    now,
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "iccid"}},
			DoUpdates: clause.Assignments(map[string]any{
				"imsi":         imsi,
				"operator":     operator,
				"current_imei": currentIMEI,
				"last_seen":    now,
				"updated_at":   now,
			}),
		}).Create(&sim).Error; err != nil {
			return err
		}
		if imsi == "" {
			return nil
		}
		return upsertSIMSubscriptionIdentity(tx, imsi, iccid, operator, now)
	})
}

func upsertSIMSubscriptionIdentity(tx *gorm.DB, imsi, iccid, operator string, now time.Time) error {
	updates := map[string]any{
		"current_iccid": iccid,
		"operator":      operator,
		"last_seen":     now,
		"updated_at":    now,
	}
	row := SIMSubscription{
		IMSI:         imsi,
		CurrentICCID: iccid,
		Operator:     operator,
		LastSeen:     now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "imsi"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&row).Error
}

// UpdateSIMCardPhoneNumberByIMSI 通过 IMSI 写入/更新手机号。
// 在收到短信/注册成功时从协议层学习本机号码后调用。
// 若尚无订阅行，会自动补建最小订阅记录。
func UpdateSIMCardPhoneNumberByIMSI(imsi, phone string) error {
	return UpdateSIMCardVoWiFiPhoneNumberByIMSI(imsi, phone)
}

func UpdateSIMCardModemPhoneNumberByIMSI(imsi, phone string) error {
	return updateSIMCardPhoneNumberByIMSI(imsi, phone, "modem")
}

func UpdateSIMCardVoWiFiPhoneNumberByIMSI(imsi, phone string) error {
	return updateSIMCardPhoneNumberByIMSI(imsi, phone, "vowifi")
}

func SetSIMCardManualPhoneNumberByIMSI(imsi, phone string) error {
	return updateSIMCardPhoneNumberByIMSI(imsi, phone, "manual")
}

func updateSIMCardPhoneNumberByIMSI(imsi, phone, source string) error {
	imsi = strings.TrimSpace(imsi)
	if imsi == "" || DB == nil {
		return nil
	}

	normalized := normalizeSIMPhoneNumber(phone)
	if normalized == "" && !(source == "manual" && strings.TrimSpace(phone) == "") {
		return nil
	}
	if phoneDigitsEqualIMSI(normalized, imsi) {
		return nil
	}

	now := time.Now()
	column := "modem_phone_number"
	switch source {
	case "manual":
		column = "manual_phone_number"
	case "vowifi":
		column = "vowifi_phone_number"
	}

	var latest SIMSubscription
	err := DB.Where("imsi = ?", imsi).Limit(1).First(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	switch source {
	case "manual":
		latest.ManualPhoneNumber = normalized
	case "vowifi":
		latest.VowifiPhoneNumber = normalized
	default:
		latest.ModemPhoneNumber = normalized
	}
	finalPhone := bestSIMSubscriptionPhone(latest)

	updates := map[string]interface{}{
		column:         normalized,
		"phone_number": finalPhone,
		"last_seen":    now,
		"updated_at":   now,
	}

	row := SIMSubscription{
		IMSI:        imsi,
		PhoneNumber: finalPhone,
		LastSeen:    now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	switch source {
	case "manual":
		row.ManualPhoneNumber = normalized
	case "vowifi":
		row.VowifiPhoneNumber = normalized
	default:
		row.ModemPhoneNumber = normalized
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "imsi"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&row).Error
}

// updatePendingPhoneByICCID 与 updateSIMCardPhoneNumberByIMSI 同构，但按 ICCID 暂存。
// 优先级同样 manual > vowifi > modem。
func updatePendingPhoneByICCID(iccid, phone, source string) error {
	iccid = strings.TrimSpace(iccid)
	if iccid == "" || DB == nil {
		return nil
	}
	normalized := normalizeSIMPhoneNumber(phone)
	if normalized == "" && !(source == "manual" && strings.TrimSpace(phone) == "") {
		return nil
	}
	now := time.Now()
	column := "modem_phone_number"
	switch source {
	case "manual":
		column = "manual_phone_number"
	case "vowifi":
		column = "vowifi_phone_number"
	}
	var latest PendingPhoneNumber
	err := DB.Where("iccid = ?", iccid).Limit(1).First(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	switch source {
	case "manual":
		latest.ManualPhoneNumber = normalized
	case "vowifi":
		latest.VowifiPhoneNumber = normalized
	default:
		latest.ModemPhoneNumber = normalized
	}
	finalPhone := bestPendingPhone(latest)
	updates := map[string]interface{}{
		column:         normalized,
		"phone_number": finalPhone,
		"updated_at":   now,
	}
	row := PendingPhoneNumber{ICCID: iccid, PhoneNumber: finalPhone, CreatedAt: now, UpdatedAt: now}
	switch source {
	case "manual":
		row.ManualPhoneNumber = normalized
	case "vowifi":
		row.VowifiPhoneNumber = normalized
	default:
		row.ModemPhoneNumber = normalized
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "iccid"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&row).Error
}

// RecordModemPhoneNumber 路由：IMSI 已知写 sim_subscriptions，否则按 ICCID 暂存。
func RecordModemPhoneNumber(imsi, iccid, phone string) error {
	if strings.TrimSpace(imsi) != "" {
		return UpdateSIMCardModemPhoneNumberByIMSI(imsi, phone)
	}
	return updatePendingPhoneByICCID(iccid, phone, "modem")
}

// RecordVoWiFiPhoneNumber 路由：IMSI 已知写 sim_subscriptions，否则按 ICCID 暂存。
func RecordVoWiFiPhoneNumber(imsi, iccid, phone string) error {
	if strings.TrimSpace(imsi) != "" {
		return UpdateSIMCardVoWiFiPhoneNumberByIMSI(imsi, phone)
	}
	return updatePendingPhoneByICCID(iccid, phone, "vowifi")
}

func RecordManualPhoneNumber(imsi, iccid, phone string) error {
	if strings.TrimSpace(imsi) != "" {
		return SetSIMCardManualPhoneNumberByIMSI(imsi, phone)
	}
	return updatePendingPhoneByICCID(iccid, phone, "manual")
}

// migratePendingPhoneToSubscription 在 IMSI 到位后，把 ICCID 暂存的号码迁移进 sim_subscriptions，
// 复用 updateSIMCardPhoneNumberByIMSI（自带 IMSI 等值守卫），随后删除 staging 行。
func migratePendingPhoneToSubscription(imsi, iccid string) error {
	imsi = strings.TrimSpace(imsi)
	iccid = strings.TrimSpace(iccid)
	if imsi == "" || iccid == "" || DB == nil {
		return nil
	}
	var pending PendingPhoneNumber
	err := DB.Where("iccid = ?", iccid).Limit(1).First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m := normalizeSIMPhoneNumber(pending.ModemPhoneNumber); m != "" {
		if err := updateSIMCardPhoneNumberByIMSI(imsi, m, "modem"); err != nil {
			return err
		}
	}
	if v := normalizeSIMPhoneNumber(pending.VowifiPhoneNumber); v != "" {
		if err := updateSIMCardPhoneNumberByIMSI(imsi, v, "vowifi"); err != nil {
			return err
		}
	}
	if manual := normalizeSIMPhoneNumber(pending.ManualPhoneNumber); manual != "" {
		if err := updateSIMCardPhoneNumberByIMSI(imsi, manual, "manual"); err != nil {
			return err
		}
	}
	return DB.Where("iccid = ?", iccid).Delete(&PendingPhoneNumber{}).Error
}

func NormalizeSIMPhoneNumber(v string) string {
	return normalizeSIMPhoneNumber(v)
}

func normalizeSIMPhoneNumber(v string) string {
	s := canonicalLocalPhone(v)
	if s == "" {
		return ""
	}
	upper := strings.ToUpper(s)
	if upper == "FFFFFFFF" || upper == "FFFFFFFFFFFF" || upper == "00000000000" {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(v), "Own Number") {
		return ""
	}
	if allSameRune(upper, 'F') || allSameRune(s, '0') {
		return ""
	}
	if !looksLikePhoneNumber(s) {
		return ""
	}
	return s
}

func bestSIMSubscriptionPhone(sub SIMSubscription) string {
	for _, candidate := range []string{sub.ManualPhoneNumber, sub.VowifiPhoneNumber, sub.ModemPhoneNumber} {
		if phone := normalizeSIMPhoneNumber(candidate); phone != "" {
			return phone
		}
	}
	return ""
}

func bestPendingPhone(pending PendingPhoneNumber) string {
	for _, candidate := range []string{pending.ManualPhoneNumber, pending.VowifiPhoneNumber, pending.ModemPhoneNumber} {
		if phone := normalizeSIMPhoneNumber(candidate); phone != "" {
			return phone
		}
	}
	return ""
}

func allSameRune(s string, r rune) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch != r {
			return false
		}
	}
	return true
}

// UpdateDeviceCurrentSIM 更新设备当前插入的 SIM 卡
func UpdateDeviceCurrentSIM(imei string, iccid *string) error {
	return DB.Model(&Device{}).Where("imei = ?", imei).Updates(map[string]interface{}{
		"iccid":     iccid,
		"last_seen": time.Now(),
	}).Error
}

// UpdateDeviceSignal 更新设备信号强度
func UpdateDeviceSignal(imei string, signalDBM int) error {
	return DB.Model(&Device{}).Where("imei = ?", imei).Updates(map[string]interface{}{
		"signal_dbm": signalDBM,
		"last_seen":  time.Now(),
	}).Error
}

// SaveSMS 保存短信记录
// 注意：时间戳会被截断到秒精度，确保发送（time.Now() 有纳秒）和接收（SCTS 仅有秒）
// 的消息在同一秒内能通过 id 正确排序
func SaveSMS(imsi, sender, recipient, content string, smsType, status int, timestamp time.Time) error {
	return SaveSMSWithLocalPhone(imsi, "", sender, recipient, content, smsType, status, timestamp)
}

// HasDuplicateReceivedSMS 检查在指定时间窗口内是否已存在内容相同的下行接收短信。
func HasDuplicateReceivedSMS(imsi, localPhone, sender, recipient, content string, timestamp time.Time, window time.Duration) (bool, error) {
	if DB == nil {
		return false, nil
	}
	imsi = strings.TrimSpace(imsi)
	sender = strings.TrimSpace(sender)
	recipient = strings.TrimSpace(recipient)
	content = strings.TrimSpace(content)
	if imsi == "" || sender == "" || content == "" {
		return false, nil
	}
	if window <= 0 {
		window = 5 * time.Minute
	}
	ts := timestamp.Truncate(time.Second)
	start := ts.Add(-window)
	end := ts.Add(window)

	var count int64
	err := DB.Model(&SMS{}).
		Where("imsi = ? AND type = ? AND sender = ? AND recipient = ? AND content = ? AND timestamp BETWEEN ? AND ?",
			imsi, 1, sender, strings.TrimSpace(recipient), content, start, end).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SaveSMSWithLocalPhone 保存短信记录并显式写入本机号码。
// localPhone 为空时会按方向自动推导，并在必要时回退到订阅手机号。
func SaveSMSWithLocalPhone(imsi, localPhone, sender, recipient, content string, smsType, status int, timestamp time.Time) error {
	if DB == nil {
		return nil
	}
	imsi = strings.TrimSpace(imsi)
	sender = strings.TrimSpace(sender)
	recipient = strings.TrimSpace(recipient)

	peer := normalizeSMSPeer(smsType, sender, recipient)
	localPhone = normalizeSMSLocalPhone(imsi, smsType, localPhone, sender, recipient)
	// 运行时即解析 ICCID（与 P4 回填同一约定：无真实映射回退 "imsi:" 前缀），
	// 否则新短信 iccid 为空，按 ICCID 维度的查询/删除会全部落空。
	sms := SMS{
		IMSI:       imsi,
		ICCID:      GetICCIDForIMSI(imsi),
		Peer:       peer,
		LocalPhone: localPhone,
		Sender:     sender,
		Recipient:  recipient,
		Content:    content,
		Type:       smsType,
		Status:     status,
		Timestamp:  timestamp.Truncate(time.Second),
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&sms).Error; err != nil {
			return err
		}
		if peer == "" {
			return nil
		}
		return upsertSMSContactFromSMS(tx, &sms)
	})
}

func normalizeSMSPeer(smsType int, sender, recipient string) string {
	if smsType == 2 {
		p := strings.TrimSpace(recipient)
		if p != "" {
			return p
		}
	}
	return strings.TrimSpace(sender)
}

func normalizeSMSLocalPhone(imsi string, smsType int, localPhone, sender, recipient string) string {
	trimPhone := canonicalLocalPhone(localPhone)
	if looksLikePhoneNumber(trimPhone) {
		return trimPhone
	}

	var candidate string
	switch smsType {
	case 1:
		candidate = canonicalLocalPhone(recipient)
	case 2:
		candidate = canonicalLocalPhone(sender)
	}
	if looksLikePhoneNumber(candidate) {
		return candidate
	}

	if imsi != "" {
		if learned, err := GetSIMCardPhoneNumberByIMSI(imsi); err == nil {
			learned = canonicalLocalPhone(learned)
			if looksLikePhoneNumber(learned) {
				return learned
			}
		}
	}

	return strings.TrimSpace(trimPhone)
}

func canonicalLocalPhone(v string) string {
	s := strings.TrimSpace(v)
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "tel:") {
		s = strings.TrimSpace(s[4:])
		lower = strings.ToLower(s)
	}
	if strings.HasPrefix(lower, "sip:") {
		s = strings.TrimSpace(s[4:])
		if idx := strings.IndexAny(s, "@;>"); idx >= 0 {
			s = s[:idx]
		}
	}
	s = strings.Trim(s, "<>\"")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	return strings.TrimSpace(s)
}

func looksLikePhoneNumber(v string) bool {
	s := canonicalLocalPhone(v)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if len(s) < 6 || len(s) > 15 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// phoneDigits 返回仅保留数字的形式（去掉前导 + 与分隔符）。
func phoneDigits(v string) string {
	s := canonicalLocalPhone(v)
	s = strings.TrimPrefix(s, "+")
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			b = append(b, s[i])
		}
	}
	return string(b)
}

// phoneDigitsEqualIMSI 判断号码数字是否与 IMSI 数字完全相同（误学 IMSI 的特征）。
func phoneDigitsEqualIMSI(phone, imsi string) bool {
	pd := phoneDigits(phone)
	id := phoneDigits(imsi)
	return pd != "" && pd == id
}

func upsertSMSContactFromSMS(tx *gorm.DB, sms *SMS) error {
	contact := SMSContact{
		IMSI:          sms.IMSI,
		ICCID:         sms.ICCID,
		Peer:          sms.Peer,
		LastSMSID:     sms.ID,
		LastTimestamp: sms.Timestamp,
		LastContent:   sms.Content,
		LastType:      sms.Type,
		UnreadCount:   0,
	}
	isIncomingUnread := sms.Type == 1 && sms.Status == 0
	if isIncomingUnread {
		contact.UnreadCount = 1
	}

	doUpdates := clause.AssignmentColumns([]string{"iccid", "last_sms_id", "last_timestamp", "last_content", "last_type", "updated_at"})
	onConflict := clause.OnConflict{
		Columns:   []clause.Column{{Name: "imsi"}, {Name: "peer"}},
		DoUpdates: doUpdates,
	}

	if isIncomingUnread {
		onConflict.DoUpdates = clause.Assignments(map[string]any{
			"iccid":          sms.ICCID,
			"last_sms_id":    sms.ID,
			"last_timestamp": sms.Timestamp,
			"last_content":   sms.Content,
			"last_type":      sms.Type,
			"unread_count":   gorm.Expr("unread_count + 1"),
			"updated_at":     time.Now(),
		})
	}

	return tx.Clauses(onConflict).Create(&contact).Error
}

func BackfillSMSPeerAndContacts(batchSize int) error {
	if batchSize <= 0 {
		batchSize = 500
	}

	need, err := NeedBackfillSMSContacts()
	if err != nil {
		return err
	}
	if !need {
		return nil
	}

	if err := DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SMSContact{}).Error; err != nil {
		return err
	}

	lastTs := time.Time{}
	var lastID uint = 0

	for {
		var batch []SMS
		query := DB.Order("timestamp asc, id asc").Limit(batchSize)
		if !lastTs.IsZero() || lastID != 0 {
			query = query.Where("timestamp > ? OR (timestamp = ? AND id > ?)", lastTs, lastTs, lastID)
		}
		if err := query.Find(&batch).Error; err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}

		if err := DB.Transaction(func(tx *gorm.DB) error {
			for i := range batch {
				sms := &batch[i]
				if strings.TrimSpace(sms.Peer) == "" {
					sms.Peer = normalizeSMSPeer(sms.Type, sms.Sender, sms.Recipient)
					if sms.Peer != "" {
						if err := tx.Model(&SMS{}).Where("id = ?", sms.ID).Update("peer", sms.Peer).Error; err != nil {
							return err
						}
					}
				}
				if sms.Peer != "" {
					if err := upsertSMSContactFromSMS(tx, sms); err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			return err
		}

		last := batch[len(batch)-1]
		lastTs = last.Timestamp
		lastID = last.ID
	}
}

func NeedBackfillSMSContacts() (bool, error) {
	var smsCount int64
	if err := DB.Model(&SMS{}).Count(&smsCount).Error; err != nil {
		return false, err
	}
	if smsCount == 0 {
		return false, nil
	}

	var contactCount int64
	if err := DB.Model(&SMSContact{}).Count(&contactCount).Error; err != nil {
		return false, err
	}

	var missingPeer int64
	if err := DB.Model(&SMS{}).Where("peer = '' OR peer IS NULL").Count(&missingPeer).Error; err != nil {
		return false, err
	}

	return contactCount == 0 || missingPeer > 0, nil
}

// GetSMSByIMSI 获取指定 IMSI 的短信列表
func GetSMSByIMSI(imsi string, limit int) ([]SMS, error) {
	var smsList []SMS
	err := DB.Where("imsi = ?", imsi).Order("timestamp desc").Limit(limit).Find(&smsList).Error
	return smsList, err
}

// GetSMSByICCID 获取指定 ICCID 的短信列表（P4 ICCID 维度读取）。
func GetSMSByICCID(iccid string, limit int) ([]SMS, error) {
	var smsList []SMS
	err := DB.Where("iccid = ?", iccid).Order("timestamp desc").Limit(limit).Find(&smsList).Error
	return smsList, err
}

// GetICCIDForIMSI 从 sim_cards 查 IMSI 对应的真实 ICCID；
// 无映射或为 reader-imsi- 合成 ICCID 时返回 "imsi:" 前缀合成键，与 P4 回填逻辑对齐。
func GetICCIDForIMSI(imsi string) string {
	imsi = strings.TrimSpace(imsi)
	if imsi == "" {
		return ""
	}
	if DB == nil {
		return "imsi:" + imsi
	}
	type row struct {
		ICCID string `gorm:"column:iccid"`
	}
	var r row
	err := DB.Table("sim_cards").Select("iccid").Where("imsi = ?", imsi).First(&r).Error
	if err != nil || strings.TrimSpace(r.ICCID) == "" || strings.HasPrefix(r.ICCID, "reader-imsi-") {
		return "imsi:" + imsi
	}
	return strings.TrimSpace(r.ICCID)
}

// GetRecentSMS 获取所有 SIM 卡的最近短信列表
func GetRecentSMS(limit int) ([]SMS, error) {
	var smsList []SMS
	err := DB.Order("timestamp desc").Limit(limit).Find(&smsList).Error
	return smsList, err
}

// GetAllDevices 获取所有设备
func GetAllDevices() ([]Device, error) {
	var devices []Device
	err := DB.Find(&devices).Error
	return devices, err
}

// GetAllSIMCards 获取所有 SIM 卡
func GetAllSIMCards() ([]SIMCard, error) {
	var sims []SIMCard
	err := DB.Find(&sims).Error
	return sims, err
}

// GetSIMCardPhoneNumberByIMSI 获取 IMSI 对应的最近手机号（无则返回空字符串）。
func GetSIMCardPhoneNumberByIMSI(imsi string) (string, error) {
	imsi = strings.TrimSpace(imsi)
	if DB == nil || imsi == "" {
		return "", nil
	}

	var sub SIMSubscription
	err := DB.Select("phone_number").
		Where("imsi = ? AND COALESCE(phone_number, '') <> ''", imsi).
		Limit(1).
		First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sub.PhoneNumber), nil
}

// GetPhoneNumberByIMSIOrICCID 先按 IMSI 查 sim_subscriptions，空则按 ICCID 查 staging。
func GetPhoneNumberByIMSIOrICCID(imsi, iccid string) (string, error) {
	if phone, err := GetSIMCardPhoneNumberByIMSI(imsi); err != nil {
		return "", err
	} else if strings.TrimSpace(phone) != "" {
		return strings.TrimSpace(phone), nil
	}
	iccid = strings.TrimSpace(iccid)
	if DB == nil || iccid == "" {
		return "", nil
	}
	var pending PendingPhoneNumber
	err := DB.Select("phone_number").
		Where("iccid = ? AND COALESCE(phone_number, '') <> ''", iccid).
		Limit(1).First(&pending).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(pending.PhoneNumber), nil
}

func GetSIMPhoneNumbersByIMSI() (map[string]string, error) {
	out := map[string]string{}
	if DB == nil {
		return out, nil
	}
	var rows []SIMSubscription
	if err := DB.Select("imsi", "phone_number").
		Where("COALESCE(imsi, '') <> '' AND COALESCE(phone_number, '') <> ''").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		imsi := strings.TrimSpace(row.IMSI)
		phone := strings.TrimSpace(row.PhoneNumber)
		if imsi != "" && phone != "" {
			out[imsi] = phone
		}
	}
	return out, nil
}

// GetDevicePublicIP 获取设备当前外网 IP (PublicIP)
func GetDevicePublicIP(imei string) (string, error) {
	var device Device
	if err := DB.Select("public_ip").Where("imei = ?", imei).First(&device).Error; err != nil {
		return "", err
	}
	return device.PublicIP, nil
}

// UpdateDeviceIPs 更新设备当前 IP (PublicIP 和 PrivateIP)
func UpdateDeviceIPs(imei, publicIP, privateIP string) error {
	updates := map[string]interface{}{
		"last_seen": time.Now(),
	}
	if publicIP != "" {
		updates["public_ip"] = publicIP
	}
	if privateIP != "" {
		updates["private_ip"] = privateIP
	}
	return DB.Model(&Device{}).Where("imei = ?", imei).Updates(updates).Error
}

// UpdateDeviceIPsV6 updates v4/v6 public and private addresses; empty values do not overwrite existing fields.
func UpdateDeviceIPsV6(imei, publicV4, publicV6, privateV4, privateV6 string) error {
	updates := map[string]interface{}{
		"last_seen": time.Now(),
	}
	if publicV4 != "" {
		updates["public_ip"] = publicV4
	}
	if publicV6 != "" {
		updates["public_ipv6"] = publicV6
	}
	if privateV4 != "" {
		updates["private_ip"] = privateV4
	}
	if privateV6 != "" {
		updates["private_ipv6"] = privateV6
	}
	return DB.Model(&Device{}).Where("imei = ?", imei).Updates(updates).Error
}

func rebuildSMSContactTx(tx *gorm.DB, imsi, peer string) (bool, error) {
	imsi = strings.TrimSpace(imsi)
	peer = strings.TrimSpace(peer)
	if imsi == "" || peer == "" {
		return true, nil
	}

	var latest SMS
	err := tx.Where("imsi = ? AND peer = ?", imsi, peer).
		Order("timestamp desc, id desc").
		First(&latest).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Where("imsi = ? AND peer = ?", imsi, peer).Delete(&SMSContact{}).Error; err != nil {
				return true, err
			}
			return true, nil
		}
		return false, err
	}

	var unreadCount int64
	if err := tx.Model(&SMS{}).
		Where("imsi = ? AND peer = ? AND type = ? AND status = ?", imsi, peer, 1, 0).
		Count(&unreadCount).Error; err != nil {
		return false, err
	}

	now := time.Now()
	contact := SMSContact{
		IMSI:          imsi,
		ICCID:         latest.ICCID,
		Peer:          peer,
		LastSMSID:     latest.ID,
		LastTimestamp: latest.Timestamp,
		LastContent:   latest.Content,
		LastType:      latest.Type,
		UnreadCount:   int(unreadCount),
		UpdatedAt:     now,
	}
	return false, tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "imsi"}, {Name: "peer"}},
		DoUpdates: clause.Assignments(map[string]any{
			"iccid":          contact.ICCID,
			"last_sms_id":    contact.LastSMSID,
			"last_timestamp": contact.LastTimestamp,
			"last_content":   contact.LastContent,
			"last_type":      contact.LastType,
			"unread_count":   contact.UnreadCount,
			"updated_at":     now,
		}),
	}).Create(&contact).Error
}

func RebuildSMSContact(imsi, peer string) (bool, error) {
	if DB == nil {
		return true, nil
	}
	var threadEmpty bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		threadEmpty, err = rebuildSMSContactTx(tx, imsi, peer)
		return err
	})
	return threadEmpty, err
}

func DeleteSMSByID(id uint) (bool, string, string, error) {
	if DB == nil {
		return true, "", "", ErrSMSNotFound
	}

	var (
		threadEmpty bool
		imsi        string
		peer        string
	)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var sms SMS
		if err := tx.Where("id = ?", id).First(&sms).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSMSNotFound
			}
			return err
		}
		imsi = strings.TrimSpace(sms.IMSI)
		peer = strings.TrimSpace(sms.Peer)
		if err := tx.Delete(&SMS{}, id).Error; err != nil {
			return err
		}
		var err error
		threadEmpty, err = rebuildSMSContactTx(tx, imsi, peer)
		return err
	})
	return threadEmpty, imsi, peer, err
}

func DeleteSMSByIMSIAndPeer(imsi, peer string) (int64, error) {
	if DB == nil {
		return 0, ErrSMSNotFound
	}
	imsi = strings.TrimSpace(imsi)
	peer = strings.TrimSpace(peer)
	if imsi == "" || peer == "" {
		return 0, ErrSMSNotFound
	}

	var deleted int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("imsi = ? AND peer = ?", imsi, peer).Delete(&SMS{})
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		if deleted == 0 {
			return ErrSMSNotFound
		}
		return tx.Where("imsi = ? AND peer = ?", imsi, peer).Delete(&SMSContact{}).Error
	})
	return deleted, err
}

// DeleteSMSByICCIDAndPeer 按 ICCID 删除会话（P4 ICCID 维度写入）。
func DeleteSMSByICCIDAndPeer(iccid, peer string) (int64, error) {
	if DB == nil {
		return 0, ErrSMSNotFound
	}
	iccid = strings.TrimSpace(iccid)
	peer = strings.TrimSpace(peer)
	if iccid == "" || peer == "" {
		return 0, ErrSMSNotFound
	}

	var deleted int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("iccid = ? AND peer = ?", iccid, peer).Delete(&SMS{})
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected
		if deleted == 0 {
			return ErrSMSNotFound
		}
		return tx.Where("iccid = ? AND peer = ?", iccid, peer).Delete(&SMSContact{}).Error
	})
	return deleted, err
}

// CurrentICCIDForDevice 尝试通过 alias (即 device id) 或 imei 查询关联的当前 ICCID。
func CurrentICCIDForDevice(deviceID string) string {
	if DB == nil {
		return ""
	}
	var dev Device
	if err := DB.Where("alias = ? OR imei = ?", deviceID, deviceID).First(&dev).Error; err == nil {
		if dev.CurrentICCID != nil {
			return *dev.CurrentICCID
		}
	}
	return ""
}
