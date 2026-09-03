package service

import (
	"context"
	"fmt"
	"time"

	"github.com/mechta-market/pulse/internal/domain/deploy/model"
	"github.com/mechta-market/pulse/internal/errs"
)

type Service struct {
	repoDb RepoDbI
}

func New(repoDb RepoDbI) *Service {
	return &Service{repoDb: repoDb}
}

func (s *Service) List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error) {
	items, tCount, err := s.repoDb.List(ctx, pars)
	if err != nil {
		return nil, 0, fmt.Errorf("repoDb.List: %w", err)
	}
	return items, tCount, nil
}

func (s *Service) Create(ctx context.Context, obj *model.Edit) (int64, error) {
	if obj.Cluster == nil || obj.Namespace == nil || obj.Kind == nil || obj.Name == nil || obj.ServiceName == nil {
		return 0, fmt.Errorf("%w: deploy key (cluster, namespace, kind, name, service_name) is required", errs.InvalidRequest)
	}
	if obj.ObservedAt == nil {
		obj.ObservedAt = new(time.Now().UTC())
	}

	newId, err := s.repoDb.Create(ctx, obj)
	if err != nil {
		return 0, fmt.Errorf("repoDb.Create: %w", err)
	}
	return newId, nil
}
