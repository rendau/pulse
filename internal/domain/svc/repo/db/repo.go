package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mechta-market/mobone/v2"
	moboneTools "github.com/mechta-market/mobone/v2/tools"
	"github.com/samber/lo"

	commonRepoPg "github.com/rendau/pulse/internal/domain/common/repo/pg"
	"github.com/rendau/pulse/internal/domain/svc/model"
	repoModel "github.com/rendau/pulse/internal/domain/svc/repo/db/model"
)

const tableName = "service"

type Repo struct {
	*commonRepoPg.Base
	ModelStore *mobone.ModelStore
}

func New(con *pgxpool.Pool) *Repo {
	base := commonRepoPg.NewBase(con)
	return &Repo{
		Base: base,
		ModelStore: &mobone.ModelStore{
			Con:       base.Con,
			QB:        base.QB,
			TableName: tableName,
		},
	}
}

func (r *Repo) List(ctx context.Context, pars *model.ListReq) ([]*model.Main, int64, error) {
	conditions, conditionExps := r.getConditions(pars)
	sort := moboneTools.ConstructSortColumns(allowedSortFields, pars.Sort)
	items := make([]*repoModel.Select, 0)

	totalCount, err := r.ModelStore.List(ctx, mobone.ListParams{
		Conditions:           conditions,
		ConditionExpressions: conditionExps,
		Page:                 pars.Page,
		PageSize:             pars.PageSize,
		WithTotalCount:       pars.WithTotalCount,
		OnlyCount:            pars.OnlyCount,
		Sort:                 sort,
	}, func(add bool) mobone.ListModelI {
		item := &repoModel.Select{}
		if add {
			items = append(items, item)
		}
		return item
	})
	if err != nil {
		return nil, 0, fmt.Errorf("ModelStore.List: %w", err)
	}

	return lo.Map(items, repoModel.EncodeSelect), totalCount, nil
}

func (r *Repo) Get(ctx context.Context, name string) (*model.Main, bool, error) {
	m := &repoModel.Select{Name: name}

	found, err := r.ModelStore.Get(ctx, m)
	if err != nil {
		return nil, false, fmt.Errorf("ModelStore.Get: %w", err)
	}
	if !found {
		return nil, false, nil
	}

	return repoModel.EncodeSelect(m, 0), true, nil
}

func (r *Repo) UpdateOrCreate(ctx context.Context, obj *model.Edit) error {
	m, err := repoModel.DecodeUpsert(obj)
	if err != nil {
		return fmt.Errorf("DecodeUpsert: %w", err)
	}

	if err = r.ModelStore.UpdateOrCreate(ctx, m); err != nil {
		return fmt.Errorf("ModelStore.UpdateOrCreate: %w", err)
	}

	return nil
}

func (r *Repo) Delete(ctx context.Context, name string) error {
	if err := r.ModelStore.Delete(ctx, &repoModel.Upsert{PKName: name}); err != nil {
		return fmt.Errorf("ModelStore.Delete: %w", err)
	}
	return nil
}

func (r *Repo) DeleteStale(ctx context.Context, before time.Time) ([]string, error) {
	query, args, err := r.QB.
		Delete(tableName).
		Where("last_seen < ?", before).
		Suffix("returning name").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("DeleteStale build query: %w", err)
	}

	rows, err := r.Con.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("DeleteStale: %w", err)
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("DeleteStale scan: %w", err)
		}
		names = append(names, name)
	}

	return names, rows.Err()
}
