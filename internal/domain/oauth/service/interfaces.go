package service

import (
	"context"
	"time"

	"github.com/rendau/pulse/internal/domain/oauth/model"
)

type RepoDbI interface {
	GetGrant(ctx context.Context, id string) (*model.Grant, bool, error)
	CreateGrant(ctx context.Context, obj *model.Grant) (string, error)
	DeleteGrant(ctx context.Context, id string) error
	MarkGrantConnected(ctx context.Context, id string) error
	TouchGrantLastUsed(ctx context.Context, id string) error

	CreateTokens(ctx context.Context, tokens []*model.Token) error
	GetToken(ctx context.Context, hash string) (*model.Token, bool, error)
	TakeToken(ctx context.Context, hash, kind string) (*model.Token, bool, error)

	Cleanup(ctx context.Context, grantsCreatedBefore time.Time) (tokens, grants int64, err error)
}
