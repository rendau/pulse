package github

import (
	"context"
	"time"

	githubModel "github.com/mechta-market/pulse/internal/service/github/model"
)

// Client — read-only доступ к GitHub REST API.
type Client interface {
	// GetFileContent читает файл из ветки по умолчанию. Отсутствие файла — found=false, не ошибка.
	GetFileContent(ctx context.Context, repoUrl, path string) ([]byte, bool, error)
	// RepoInfo — ветка по умолчанию, описание и topics репозитория (кэш на несколько часов).
	RepoInfo(ctx context.Context, repoUrl string) (*githubModel.Repo, error)
	// ListCommits — коммиты ветки по умолчанию за интервал, новые первыми, не более limit.
	ListCommits(ctx context.Context, repoUrl string, since, until time.Time, limit int) ([]githubModel.Commit, error)
	// CompareCommits — что есть в ветке по умолчанию сверх base (задеплоенного SHA).
	CompareCommits(ctx context.Context, repoUrl, base string) (*githubModel.Comparison, error)
	// ResolveImageCommit — коммит сборки образа по digest через пакеты ghcr и запуски Actions;
	// пустая строка — сборка не найдена однозначно.
	ResolveImageCommit(ctx context.Context, repoUrl, imagePath, digest string) (string, error)
	// PackageRepoUrl — репозиторий, к которому привязан контейнерный пакет ghcr; пустая
	// строка — пакет не найден или не привязан.
	PackageRepoUrl(ctx context.Context, imagePath string) (string, error)
	Ping(ctx context.Context) error
}
