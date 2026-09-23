// Package imageref — разбор ссылок на container image.
package imageref

import (
	"fmt"
	"regexp"
	"strings"
)

// Ref — разобранная ссылка на образ.
type Ref struct {
	Host string // ghcr.io
	Path string // mechta-market/promo-sync
	Tag  string // latest; пусто, если задан только digest
	// Digest — sha256:…; пусто, если ссылка по тегу
	Digest string
}

// Name — последний сегмент пути: имя образа без org и registry.
func (r Ref) Name() string {
	if i := strings.LastIndex(r.Path, "/"); i >= 0 {
		return r.Path[i+1:]
	}
	return r.Path
}

// Repo — первые два сегмента пути: репозиторий, из которого собран образ. GitHub Packages
// кладёт образы внутрь репозитория (ghcr.io/rendau/loom/server → rendau/loom), поэтому
// путь целиком репозиторием не является. Односегментный путь возвращается как есть.
func (r Ref) Repo() string {
	parts := strings.SplitN(r.Path, "/", 3)
	if len(parts) < 2 {
		return r.Path
	}
	return parts[0] + "/" + parts[1]
}

// RepoName — имя репозитория (второй сегмент пути): rendau/loom/server → loom.
// Для односегментного пути — само имя образа.
func (r Ref) RepoName() string {
	if _, name, ok := strings.Cut(r.Repo(), "/"); ok {
		return name
	}
	return r.Name()
}

// Org — первый сегмент пути (owner/organization).
func (r Ref) Org() string {
	if org, _, ok := strings.Cut(r.Path, "/"); ok {
		return org
	}
	return ""
}

// Reference — digest, если есть, иначе тег (по умолчанию latest).
func (r Ref) Reference() string {
	if r.Digest != "" {
		return r.Digest
	}
	if r.Tag != "" {
		return r.Tag
	}
	return "latest"
}

// String — host/path@digest либо host/path:tag.
func (r Ref) String() string {
	if r.Digest != "" {
		return r.Host + "/" + r.Path + "@" + r.Digest
	}
	return r.Host + "/" + r.Path + ":" + r.Reference()
}

// WithDigest возвращает копию ссылки с заданным digest (для запросов в registry).
func (r Ref) WithDigest(digest string) Ref {
	r.Digest = digest
	return r
}

// Parse разбирает «host/path[:tag][@sha256:…]». Ссылка без хоста (postgres:17)
// считается образом docker.io/library/….
func Parse(ref string) (Ref, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Ref{}, fmt.Errorf("empty image ref")
	}

	result := Ref{}
	if name, digest, ok := strings.Cut(ref, "@"); ok {
		ref, result.Digest = name, digest
	}

	// тег — после последнего ':', если после него нет '/' (иначе это порт хоста)
	if i := strings.LastIndex(ref, ":"); i > 0 && !strings.Contains(ref[i:], "/") {
		ref, result.Tag = ref[:i], ref[i+1:]
	}

	host, path, ok := strings.Cut(ref, "/")
	if !ok || !looksLikeHost(host) {
		host, path = "docker.io", ref
		if !strings.Contains(path, "/") {
			path = "library/" + path
		}
	}
	if path == "" {
		return Ref{}, fmt.Errorf("image ref %q: empty path", ref)
	}

	result.Host, result.Path = host, path

	return result, nil
}

// looksLikeHost — первый сегмент считается хостом, если содержит точку или порт,
// либо это localhost (как в docker/distribution reference).
func looksLikeHost(s string) bool {
	return strings.ContainsAny(s, ".:") || s == "localhost"
}

var commitShaRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// LooksLikeCommit — тег похож на SHA коммита (7–40 hex-символов).
func LooksLikeCommit(tag string) bool {
	return commitShaRe.MatchString(tag)
}

// DigestFromImageID извлекает digest из imageID статуса контейнера
// («ghcr.io/org/app@sha256:…» или «docker-pullable://…@sha256:…»).
func DigestFromImageID(imageID string) string {
	if _, digest, ok := strings.Cut(imageID, "@"); ok && strings.HasPrefix(digest, "sha256:") {
		return digest
	}
	if strings.HasPrefix(imageID, "sha256:") {
		return imageID
	}
	return ""
}
