package service

import (
	"context"

	"github.com/rendau/pulse/internal/domain/deploy/model"
)

type RepoDbI interface {
	List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error)
	Create(ctx context.Context, obj *model.Edit) (int64, error)
}
