package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rendau/pulse/internal/domain/svc/model"
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

func (s *Service) Get(ctx context.Context, name string, errNE bool) (*model.Main, bool, error) {
	result, found, err := s.repoDb.Get(ctx, name)
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

// UpdateOrCreate — единственный путь записи: каталог производный, правится только индексером.
func (s *Service) UpdateOrCreate(ctx context.Context, obj *model.Edit) error {
	if obj.Name == nil || *obj.Name == "" {
		return fmt.Errorf("%w: name is required", errs.InvalidRequest)
	}
	if obj.LastSeen == nil {
		obj.LastSeen = new(time.Now().UTC())
	}

	if err := s.repoDb.UpdateOrCreate(ctx, obj); err != nil {
		return fmt.Errorf("repoDb.UpdateOrCreate: %w", err)
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, name string) error {
	if err := s.repoDb.Delete(ctx, name); err != nil {
		return fmt.Errorf("repoDb.Delete: %w", err)
	}
	return nil
}

func (s *Service) DeleteStale(ctx context.Context, before time.Time) ([]string, error) {
	names, err := s.repoDb.DeleteStale(ctx, before)
	if err != nil {
		return nil, fmt.Errorf("repoDb.DeleteStale: %w", err)
	}
	return names, nil
}
