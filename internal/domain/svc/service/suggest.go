package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
	"github.com/mechta-market/pulse/internal/domain/svc/model"
	"github.com/mechta-market/pulse/internal/errs"
)

// clusterNameExact — уверенность точного совпадения с именем в кластере (scoreService)
const clusterNameExact = 0.93

// GetOrSuggest ищет сервис по точному имени (или однозначному имени в кластере); неизвестное имя — ошибка со списком
// похожих, а не пустой результат.
func (s *Service) GetOrSuggest(ctx context.Context, name string) (*model.Main, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: service is required", errs.InvalidRequest)
	}

	service, found, err := s.Get(ctx, name, false)
	if err != nil {
		return nil, err
	}
	if found {
		return service, nil
	}

	candidates, err := s.Resolve(ctx, name)
	if err != nil {
		return nil, err
	}

	// имя k8s Service или приложения ruto (ocenter → orders-center) — тот же сервис, если он один
	byClusterName := lo.Filter(candidates, func(c *model.Candidate, _ int) bool {
		return c.MatchedBy == constant.MatchedByClusterName && c.Confidence >= clusterNameExact
	})
	if len(byClusterName) == 1 {
		return byClusterName[0].Service, nil
	}

	desc := fmt.Sprintf("unknown service %q", name)
	if len(candidates) > 0 {
		similar := lo.Map(candidates, func(c *model.Candidate, _ int) string { return c.Service.Name })
		desc += "; similar: " + strings.Join(similar, ", ") + " (use resolve_service to pick one)"
	} else {
		desc += "; no similar names in catalog (use list_services)"
	}

	return nil, errs.ErrFull{Err: errs.ObjectNotFound, Desc: desc}
}
