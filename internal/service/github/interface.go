package github

import "context"

// Client — read-only доступ к GitHub REST API.
type Client interface {
	// GetFileContent читает файл из ветки по умолчанию. Отсутствие файла — found=false, не ошибка.
	GetFileContent(ctx context.Context, repoUrl, path string) ([]byte, bool, error)
	Ping(ctx context.Context) error
}
