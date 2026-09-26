package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rendau/pulse/internal/domain/workload/model"
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

func (s *Service) Get(ctx context.Context, key model.Key, errNE bool) (*model.Main, bool, error) {
	result, found, err := s.repoDb.Get(ctx, key)
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.Get: %w", err)
	}
	if !found {
		if errNE {
			return nil, false, errs.ObjectNotFound
		}
		return nil, false, nil
	}
	return result, true, nil
}

// UpdateOrCreateMany — единственный путь записи: топология пишется только индексером.
func (s *Service) UpdateOrCreateMany(ctx context.Context, objs []*model.Edit) error {
	now := time.Now().UTC()
	for _, obj := range objs {
		if obj.Cluster == nil || obj.Namespace == nil || obj.Kind == nil || obj.Name == nil {
			return fmt.Errorf("%w: workload key (cluster, namespace, kind, name) is required", errs.InvalidRequest)
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
