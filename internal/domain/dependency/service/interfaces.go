package service

import (
	"context"
	"time"

	"github.com/rendau/pulse/internal/domain/dependency/model"
)

type RepoDbI interface {
	List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error)
	UpdateOrCreateMany(ctx context.Context, objs []*model.Edit) error
	DeleteStale(ctx context.Context, cluster string, before time.Time) (int64, error)
}
