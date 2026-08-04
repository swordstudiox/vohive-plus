package repo

import (
	"context"

	"github.com/swordstudiox/vohive-plus/internal/config"
)

type ProxyInstanceRepository interface {
	List(ctx context.Context) ([]config.ProxyInstance, error)
	Get(ctx context.Context, id string) (*config.ProxyInstance, error)
	ReplaceAll(ctx context.Context, instances []config.ProxyInstance) error
}
