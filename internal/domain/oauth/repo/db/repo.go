package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mechta-market/mobone/v2"

	commonRepoPg "github.com/rendau/pulse/internal/domain/common/repo/pg"
	domainModel "github.com/rendau/pulse/internal/domain/oauth/model"
	repoModel "github.com/rendau/pulse/internal/domain/oauth/repo/db/model"
)

const tableName = "oauth_grant"

type Repo struct {
	*commonRepoPg.Base
	ModelStore *mobone.ModelStore
}

// New: txm — общий менеджер транзакций с usecase (обмен кода на токены идёт в одной транзакции).
func New(con *pgxpool.Pool, txm *mobone.TransactionManager) *Repo {
	base := commonRepoPg.NewBase(con)
	return &Repo{
		Base: base,
		ModelStore: &mobone.ModelStore{
			Con:                base.Con,
			TransactionManager: txm,
			QB:                 base.QB,
			TableName:          tableName,
		},
	}
}

func (r *Repo) GetGrant(ctx context.Context, id string) (*domainModel.Grant, bool, error) {
	m := &repoModel.GrantSelect{PKId: id}

	found, err := r.ModelStore.Get(ctx, m)
	if err != nil {
		return nil, false, fmt.Errorf("ModelStore.Get: %w", err)
	}
	if !found {
		return nil, false, nil
	}

	return repoModel.EncodeGrantSelect(m), true, nil
}

func (r *Repo) CreateGrant(ctx context.Context, obj *domainModel.Grant) (string, error) {
	m := repoModel.DecodeGrantUpsert(obj)

	if err := r.ModelStore.Create(ctx, m); err != nil {
		return "", fmt.Errorf("ModelStore.Create: %w", err)
	}

	return m.PKId, nil
}

func (r *Repo) DeleteGrant(ctx context.Context, id string) error {
	if err := r.ModelStore.Delete(ctx, &repoModel.GrantUpsert{PKId: id}); err != nil {
		return fmt.Errorf("ModelStore.Delete: %w", err)
	}

	return nil
}

func (r *Repo) MarkGrantConnected(ctx context.Context, id string) error {
	_, err := r.ModelStore.GetConnection(ctx).Exec(
		ctx,
		"update oauth_grant set connected_at = coalesce(connected_at, now()) where id = $1",
		id,
	)
	if err != nil {
		return fmt.Errorf("MarkGrantConnected: %w", err)
	}

	return nil
}

func (r *Repo) TouchGrantLastUsed(ctx context.Context, id string) error {
	_, err := r.ModelStore.GetConnection(ctx).Exec(ctx, "update oauth_grant set last_used_at = now() where id = $1", id)
	if err != nil {
		return fmt.Errorf("TouchGrantLastUsed: %w", err)
	}

	return nil
}

func (r *Repo) CreateTokens(ctx context.Context, tokens []*domainModel.Token) error {
	qb := r.QB.Insert("oauth_token").Columns("token_hash", "grant_id", "kind", "expires_at")
	for _, t := range tokens {
		qb = qb.Values(t.Hash, t.GrantId, t.Kind, t.ExpiresAt)
	}

	query, args, err := qb.ToSql()
	if err != nil {
		return fmt.Errorf("ToSql: %w", err)
	}

	if _, err = r.ModelStore.GetConnection(ctx).Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("CreateTokens: %w", err)
	}

	return nil
}

// GetToken — токен по хэшу, истёкший или нет.
func (r *Repo) GetToken(ctx context.Context, hash string) (*domainModel.Token, bool, error) {
	result := &domainModel.Token{Hash: hash}

	err := r.ModelStore.GetConnection(ctx).QueryRow(
		ctx,
		"select grant_id, kind, expires_at from oauth_token where token_hash = $1",
		hash,
	).Scan(&result.GrantId, &result.Kind, &result.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("QueryRow: %w", err)
	}

	return result, true, nil
}

// TakeToken удаляет токен вида kind и возвращает его: коды и refresh-токены одноразовые, два
// одновременных обмена не могут выиграть оба.
func (r *Repo) TakeToken(ctx context.Context, hash, kind string) (*domainModel.Token, bool, error) {
	result := &domainModel.Token{Hash: hash, Kind: kind}

	err := r.ModelStore.GetConnection(ctx).QueryRow(
		ctx,
		"delete from oauth_token where token_hash = $1 and kind = $2 returning grant_id, expires_at",
		hash, kind,
	).Scan(&result.GrantId, &result.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("QueryRow: %w", err)
	}

	return result, true, nil
}

// Cleanup удаляет истёкшие токены, затем подключения без токенов, созданные раньше
// grantsCreatedBefore (брошенные согласия и истёкшие подключения).
func (r *Repo) Cleanup(ctx context.Context, grantsCreatedBefore time.Time) (tokens, grants int64, err error) {
	con := r.ModelStore.GetConnection(ctx)

	tag, err := con.Exec(ctx, "delete from oauth_token where expires_at < now()")
	if err != nil {
		return 0, 0, fmt.Errorf("Cleanup tokens: %w", err)
	}
	tokens = tag.RowsAffected()

	tag, err = con.Exec(
		ctx,
		`delete from oauth_grant g
		 where g.created_at < $1
		   and not exists (select 1 from oauth_token t where t.grant_id = g.id)`,
		grantsCreatedBefore,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("Cleanup grants: %w", err)
	}
	grants = tag.RowsAffected()

	return tokens, grants, nil
}
