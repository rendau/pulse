package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rendau/pulse/internal/domain/oauth/model"
	"github.com/rendau/pulse/internal/errs"
)

type Service struct {
	repoDb RepoDbI
}

func New(repoDb RepoDbI) *Service { return &Service{repoDb: repoDb} }

func (s *Service) GetGrant(ctx context.Context, id string, errNE bool) (*model.Grant, bool, error) {
	result, found, err := s.repoDb.GetGrant(ctx, id)
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.GetGrant: %w", err)
	}
	if !found {
		if errNE {
			return nil, false, errs.ObjectNotFound
		}
		return nil, false, nil
	}
	return result, true, nil
}

func (s *Service) CreateGrant(ctx context.Context, obj *model.Grant) (string, error) {
	newId, err := s.repoDb.CreateGrant(ctx, obj)
	if err != nil {
		return "", fmt.Errorf("repoDb.CreateGrant: %w", err)
	}
	return newId, nil
}

func (s *Service) DeleteGrant(ctx context.Context, id string) error {
	if err := s.repoDb.DeleteGrant(ctx, id); err != nil {
		return fmt.Errorf("repoDb.DeleteGrant: %w", err)
	}
	return nil
}

func (s *Service) MarkGrantConnected(ctx context.Context, id string) error {
	if err := s.repoDb.MarkGrantConnected(ctx, id); err != nil {
		return fmt.Errorf("repoDb.MarkGrantConnected: %w", err)
	}
	return nil
}

func (s *Service) TouchGrantLastUsed(ctx context.Context, id string) error {
	if err := s.repoDb.TouchGrantLastUsed(ctx, id); err != nil {
		return fmt.Errorf("repoDb.TouchGrantLastUsed: %w", err)
	}
	return nil
}

// IssueToken создаёт токен вида kind для подключения и возвращает его; хранится только хэш.
func (s *Service) IssueToken(ctx context.Context, grantId, kind string, ttl time.Duration) (string, error) {
	tokens, err := s.IssueTokens(ctx, grantId, map[string]time.Duration{kind: ttl})
	if err != nil {
		return "", err
	}
	return tokens[kind], nil
}

// IssueTokens создаёт по токену на вид со своим ttl одной вставкой.
func (s *Service) IssueTokens(ctx context.Context, grantId string, ttls map[string]time.Duration) (map[string]string, error) {
	now := time.Now()
	result := make(map[string]string, len(ttls))
	records := make([]*model.Token, 0, len(ttls))

	for kind, ttl := range ttls {
		token, hash, err := GenerateToken(kind)
		if err != nil {
			return nil, fmt.Errorf("GenerateToken: %w", err)
		}
		result[kind] = token
		records = append(records, &model.Token{
			Hash:      hash,
			GrantId:   grantId,
			Kind:      kind,
			ExpiresAt: now.Add(ttl),
		})
	}

	if err := s.repoDb.CreateTokens(ctx, records); err != nil {
		return nil, fmt.Errorf("repoDb.CreateTokens: %w", err)
	}

	return result, nil
}

// FindToken — живой (не истёкший) токен вида kind.
func (s *Service) FindToken(ctx context.Context, token, kind string) (*model.Token, bool, error) {
	item, found, err := s.repoDb.GetToken(ctx, HashToken(token))
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.GetToken: %w", err)
	}
	if !found || item.Kind != kind || !item.ExpiresAt.After(time.Now()) {
		return nil, false, nil
	}
	return item, true, nil
}

// FindAnyToken — токен любого вида, истёкший или нет.
func (s *Service) FindAnyToken(ctx context.Context, token string) (*model.Token, bool, error) {
	item, found, err := s.repoDb.GetToken(ctx, HashToken(token))
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.GetToken: %w", err)
	}
	return item, found, nil
}

// TakeToken забирает одноразовый токен (код или refresh) и возвращает его, если он был живым.
func (s *Service) TakeToken(ctx context.Context, token, kind string) (*model.Token, bool, error) {
	item, found, err := s.repoDb.TakeToken(ctx, HashToken(token), kind)
	if err != nil {
		return nil, false, fmt.Errorf("repoDb.TakeToken: %w", err)
	}
	if !found || !item.ExpiresAt.After(time.Now()) {
		return nil, false, nil
	}
	return item, true, nil
}

func (s *Service) Cleanup(ctx context.Context, grantsCreatedBefore time.Time) (tokens, grants int64, err error) {
	tokens, grants, err = s.repoDb.Cleanup(ctx, grantsCreatedBefore)
	if err != nil {
		return 0, 0, fmt.Errorf("repoDb.Cleanup: %w", err)
	}
	return tokens, grants, nil
}
