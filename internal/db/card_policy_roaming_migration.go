package db

import "gorm.io/gorm"

// InitializeCardPolicyRoamingDefault protects upgrades from older databases:
// before roaming_enabled existed, the product behavior was effectively "auto".
func InitializeCardPolicyRoamingDefault(tx *gorm.DB) error {
	if tx == nil || !tx.Migrator().HasTable(&CardPolicy{}) ||
		!tx.Migrator().HasColumn(&CardPolicy{}, "roaming_enabled") {
		return nil
	}
	return tx.Model(&CardPolicy{}).Where("1 = 1").Update("roaming_enabled", true).Error
}
