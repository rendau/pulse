package service

import (
	"context"
	"fmt"
	"time"

	githubModel "github.com/rendau/pulse/internal/service/github/model"
)

// repoInfoTtl — описание и topics меняются редко: индексер ходит за ними раз в несколько
// часов, а не каждый цикл (лимит GitHub API — 5000 запросов в час на токен).
const repoInfoTtl = 6 * time.Hour

type repoCacheEntry struct {
	info      githubModel.Repo
	fetchedAt time.Time
}

// RepoInfo — ветка по умолчанию, описание и topics репозитория (кэш repoInfoTtl).
func (s *Service) RepoInfo(ctx context.Context, repoUrl string) (*githubModel.Repo, error) {
	owner, repo, err := ParseRepoUrl(repoUrl)
	if err != nil {
		return nil, err
	}
	return s.repoInfo(ctx, owner, repo)
}

func (s *Service) repoInfo(ctx context.Context, owner, repo string) (*githubModel.Repo, error) {
	key := owner + "/" + repo
	if cached, ok := s.repos.Load(key); ok {
		if entry := cached.(repoCacheEntry); time.Since(entry.fetchedAt) < repoInfoTtl {
			return new(entry.info), nil
		}
	}

	repository, _, err := s.client.Repositories.Get(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("Repositories.Get(%s): %w", key, err)
	}

	info := githubModel.Repo{
		DefaultBranch: repository.GetDefaultBranch(),
		Description:   repository.GetDescription(),
		Topics:        repository.Topics,
	}
	if info.DefaultBranch == "" {
		info.DefaultBranch = "main"
	}
	s.repos.Store(key, repoCacheEntry{info: info, fetchedAt: time.Now()})

	return new(info), nil
}
