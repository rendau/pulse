package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rendau/pulse/internal/domain/dependency/model"
	"github.com/rendau/pulse/internal/errs"
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

// UpdateOrCreateMany — единственный путь записи: связи пишет только индексер.
func (s *Service) UpdateOrCreateMany(ctx context.Context, objs []*model.Edit) error {
	now := time.Now().UTC()
	for _, obj := range objs {
		if obj.Cluster == nil || obj.FromService == nil || obj.ToHost == nil || obj.Key == nil {
			return fmt.Errorf("%w: dependency key (cluster, from_service, to_host, port, key) is required", errs.InvalidRequest)
		}
		if obj.Port == nil {
			obj.Port = new(int32(0))
		}
		if obj.LastSeen == nil {
			obj.LastSeen = &now
		}
	}

	if err := s.repoDb.UpdateOrCreateMany(ctx, objs); err != nil {
		return fmt.Errorf("repoDb.UpdateOrCreateMany: %w", err)
	}
	return nil
}

func (s *Service) DeleteStale(ctx context.Context, cluster string, before time.Time) (int64, error) {
	count, err := s.repoDb.DeleteStale(ctx, cluster, before)
	if err != nil {
		return 0, fmt.Errorf("repoDb.DeleteStale: %w", err)
	}
	return count, nil
}
