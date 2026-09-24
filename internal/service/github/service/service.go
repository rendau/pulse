package service

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/go-github/v82/github"

	"github.com/mechta-market/pulse/internal/infra/httpx"
)

type Service struct {
	client *github.Client
	// repos — кэш сведений о репозиториях: owner/repo → repoCacheEntry (repoInfo)
	repos sync.Map
	// imageCommits — digest образа → коммит сборки (imageCommit)
	imageCommits sync.Map
	// packageRepos — путь образа → репозиторий пакета (packageRepo)
	packageRepos sync.Map
}

func New(token string) *Service {
	httpClient := httpx.New(httpx.Config{Timeout: 15 * time.Second, VerifyTLS: true})

	client := github.NewClient(httpClient)
	if token != "" {
		client = client.WithAuthToken(token)
	}

	return &Service{client: client}
}

func (s *Service) Ping(ctx context.Context) error {
	if _, _, err := s.client.RateLimit.Get(ctx); err != nil {
		return fmt.Errorf("RateLimit.Get: %w", err)
	}
	return nil
}

func (s *Service) GetFileContent(ctx context.Context, repoUrl, path string) ([]byte, bool, error) {
	owner, repo, err := ParseRepoUrl(repoUrl)
	if err != nil {
		return nil, false, err
	}

	// пустой Ref — ветка по умолчанию
	file, _, resp, err := s.client.Repositories.GetContents(ctx, owner, repo, path, nil)
	if err != nil {
		if isNotFound(resp, err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("Repositories.GetContents(%s/%s, %s): %w", owner, repo, path, err)
	}
	if file == nil {
		// путь оказался директорией
		return nil, false, nil
	}

	content, err := file.GetContent()
	if err != nil {
		return nil, false, fmt.Errorf("file.GetContent: %w", err)
	}

	return []byte(content), true, nil
}

// ParseRepoUrl разбирает https://github.com/org/name(.git) → org, name.
func ParseRepoUrl(repoUrl string) (string, string, error) {
	u, err := url.Parse(repoUrl)
	if err != nil {
		return "", "", fmt.Errorf("parse repo url %q: %w", repoUrl, err)
	}

	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("repo url %q: expected github.com/<owner>/<repo>", repoUrl)
	}

	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}
