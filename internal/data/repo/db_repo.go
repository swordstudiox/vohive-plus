package repo

import (
	"context"
	"errors"

	"github.com/swordstudiox/vohive-plus/internal/config"
	"github.com/swordstudiox/vohive-plus/internal/db"

	"gorm.io/gorm"
)

type DBRepo struct{}

func NewDBRepo() *DBRepo {
	return &DBRepo{}
}

func (r *DBRepo) List(ctx context.Context) ([]config.ProxyInstance, error) {
	rows, err := db.DB.WithContext(ctx).Order("id asc").Find(&[]db.ProxyInstance{}).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []config.ProxyInstance
	for rows.Next() {
		var row db.ProxyInstance
		if err := db.DB.ScanRows(rows, &row); err != nil {
			return nil, err
		}
		inst, err := row.ToConfig()
		if err != nil {
			return nil, err
		}
		out = append(out, inst)
	}
	return out, nil
}

func (r *DBRepo) Get(ctx context.Context, id string) (*config.ProxyInstance, error) {
	row, err := db.GetProxyInstanceByID(id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	inst, err := row.ToConfig()
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (r *DBRepo) ReplaceAll(ctx context.Context, instances []config.ProxyInstance) error {
	tx := db.DB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}

	if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&db.ProxyInstance{}).Error; err != nil {
		tx.Rollback()
		return err
	}

	if len(instances) == 0 {
		return tx.Commit().Error
	}

	rows := make([]db.ProxyInstance, 0, len(instances))
	for _, inst := range instances {
		row, err := db.ProxyInstanceFromConfig(inst)
		if err != nil {
			tx.Rollback()
			return err
		}
		rows = append(rows, row)
	}

	if err := tx.CreateInBatches(rows, 50).Error; err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit().Error
}

var _ ProxyInstanceRepository = (*DBRepo)(nil)

var ErrNotInitialized = errors.New("database not initialized")
