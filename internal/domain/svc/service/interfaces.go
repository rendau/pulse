package service

import (
	"context"
	"time"

	"github.com/mechta-market/pulse/internal/domain/svc/model"
)

type RepoDbI interface {
	List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error)
	Get(ctx context.Context, name string) (*model.Main, bool, error)
	UpdateOrCreate(ctx context.Context, obj *model.Edit) error
	Delete(ctx context.Context, name string) error
	// DeleteStale удаляет сервисы, не видевшиеся с указанного момента, и возвращает их имена.
	DeleteStale(ctx context.Context, before time.Time) ([]string, error)
}
