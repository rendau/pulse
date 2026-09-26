package db

import (
	"context"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mechta-market/mobone/v2"
	moboneTools "github.com/mechta-market/mobone/v2/tools"
	"github.com/samber/lo"

	commonRepoPg "github.com/rendau/pulse/internal/domain/common/repo/pg"
	"github.com/rendau/pulse/internal/domain/workload/model"
	repoModel "github.com/rendau/pulse/internal/domain/workload/repo/db/model"
)

const tableName = "workload"

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

func (r *Repo) Get(ctx context.Context, key model.Key) (*model.Main, bool, error) {
	m := &repoModel.Select{Cluster: key.Cluster, Namespace: key.Namespace, Kind: key.Kind, Name: key.Name}

	found, err := r.ModelStore.Get(ctx, m)
	if err != nil {
		return nil, false, fmt.Errorf("ModelStore.Get: %w", err)
	}
	if !found {
		return nil, false, nil
	}

	return repoModel.EncodeSelect(m, 0), true, nil
}

func (r *Repo) UpdateOrCreateMany(ctx context.Context, objs []*model.Edit) error {
	items := lo.Map(objs, func(v *model.Edit, i int) mobone.UpdateCreateModelI {
		return repoModel.DecodeUpsert(v, i)
	})

	if err := r.ModelStore.UpdateOrCreateMany(ctx, items); err != nil {
		return fmt.Errorf("ModelStore.UpdateOrCreateMany: %w", err)
	}

	return nil
}

func (r *Repo) DeleteStale(ctx context.Context, cluster string, before time.Time) (int64, error) {
	query, args, err := r.QB.
		Delete(tableName).
		Where(sq.Eq{"cluster": cluster}).
		Where("last_seen < ?", before).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("DeleteStale build query: %w", err)
	}

	tag, err := r.Con.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("DeleteStale: %w", err)
	}

	return tag.RowsAffected(), nil
}
