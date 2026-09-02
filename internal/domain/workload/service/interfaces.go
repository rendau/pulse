package service

import (
	"context"
	"time"

	"github.com/mechta-market/pulse/internal/domain/workload/model"
)

type RepoDbI interface {
	List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error)
	Get(ctx context.Context, key model.Key) (*model.Main, bool, error)
	UpdateOrCreateMany(ctx context.Context, objs []*model.Edit) error
	// DeleteStale удаляет workloads кластера, не видевшиеся с указанного момента.
	DeleteStale(ctx context.Context, cluster string, before time.Time) (int64, error)
}
