package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	// packageRepoTTL — привязка пакета к репозиторию меняется редко
	packageRepoTTL = 24 * time.Hour
	// packageNoRepoTTL — пакет без привязки (или не найден) перепроверяется раньше
	packageNoRepoTTL = time.Hour
)

type packageRepo struct {
	url string
	at  time.Time
}

// PackageRepoUrl — репозиторий, к которому GitHub привязал контейнерный пакет (пакеты,
// опубликованные из Actions, привязываются к своей репе автоматически). Так находится
// репозиторий, когда имя образа с ним не совпадает: ghcr.io/mechta-market/dpm собирает
// mechta-market/dp-mechta, ruto-core и ruto-gateway — rendau/ruto. imagePath — путь образа
// без registry: первый сегмент — владелец, остальное — имя пакета. Пустой результат без
// ошибки — пакет не найден или ни к чему не привязан.
func (s *Service) PackageRepoUrl(ctx context.Context, imagePath string) (string, error) {
	if cached, ok := s.packageRepos.Load(imagePath); ok {
		entry := cached.(packageRepo)
		ttl := packageNoRepoTTL
		if entry.url != "" {
			ttl = packageRepoTTL
		}
		if time.Since(entry.at) < ttl {
			return entry.url, nil
		}
	}

	owner, name, ok := strings.Cut(imagePath, "/")
	if !ok || name == "" {
		return "", fmt.Errorf("image path %q: expected <owner>/<package>", imagePath)
	}

	// пакет ищется у организации, при 404 — у пользователя с тем же именем
	pkg, resp, err := s.client.Organizations.GetPackage(ctx, owner, "container", name)
	if err != nil && isNotFound(resp, err) {
		pkg, resp, err = s.client.Users.GetPackage(ctx, owner, "container", name)
	}
	if err != nil && !isNotFound(resp, err) {
		return "", fmt.Errorf("GetPackage(%s): %w", imagePath, err)
	}

	url := ""
	if fullName := pkg.GetRepository().GetFullName(); err == nil && fullName != "" {
		url = "https://github.com/" + fullName
	}
	s.packageRepos.Store(imagePath, packageRepo{url: url, at: time.Now()})

	return url, nil
}
