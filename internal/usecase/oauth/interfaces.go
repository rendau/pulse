package oauth

import (
	"context"
	"time"

	"github.com/rendau/pulse/internal/domain/oauth/model"
)

type ServiceI interface {
	GetGrant(ctx context.Context, id string, errNE bool) (*model.Grant, bool, error)
	CreateGrant(ctx context.Context, obj *model.Grant) (string, error)
	DeleteGrant(ctx context.Context, id string) error
	MarkGrantConnected(ctx context.Context, id string) error
	TouchGrantLastUsed(ctx context.Context, id string) error

	IssueToken(ctx context.Context, grantId, kind string, ttl time.Duration) (string, error)
	IssueTokens(ctx context.Context, grantId string, ttls map[string]time.Duration) (map[string]string, error)
	FindToken(ctx context.Context, token, kind string) (*model.Token, bool, error)
	FindAnyToken(ctx context.Context, token string) (*model.Token, bool, error)
	TakeToken(ctx context.Context, token, kind string) (*model.Token, bool, error)

	Cleanup(ctx context.Context, grantsCreatedBefore time.Time) (tokens, grants int64, err error)
}

type ClientCodecI interface {
	Encode(client *model.Client) (string, error)
	Decode(clientId string) (*model.Client, bool)
	Secret(clientId string) string
	VerifySecret(clientId, secret string) bool
}

type TransactionManagerI interface {
	TxFn(ctx context.Context, f func(context.Context) error) error
}
