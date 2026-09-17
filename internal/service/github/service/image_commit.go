package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v82/github"
)

const (
	// versionPages — сколько страниц версий пакета просматривать в поиске digest'а (новые первыми)
	versionPages = 5
	// runsPerPage — запусков workflow до момента публикации образа, среди которых ищется сборка
	runsPerPage = 30
	// runSlack — допуск между завершением запуска (updated_at) и временем публикации версии пакета
	runSlack = 2 * time.Minute
	// unresolvedTTL — не найденный digest не перезапрашивается в течение этого срока
	unresolvedTTL = time.Hour
)

type imageCommit struct {
	sha string
	at  time.Time
}

// ResolveImageCommit ищет коммит, из которого собран образ, без OCI-label'ов в самом образе:
// digest → версия пакета ghcr (время публикации) → push-запуск GitHub Actions репозитория,
// во время которого версия опубликована → head_sha. Пустой результат без ошибки — сборка
// не найдена однозначно (угадывать коммит нельзя). imagePath — путь образа без registry
// (mechta-market/caravan): первый сегмент — владелец пакета, остальное — имя пакета.
// Найденный коммит кэшируется навсегда (digest неизменяем), не найденный — на unresolvedTTL.
func (s *Service) ResolveImageCommit(ctx context.Context, repoUrl, imagePath, digest string) (string, error) {
	if cached, ok := s.imageCommits.Load(digest); ok {
		entry := cached.(imageCommit)
		if entry.sha != "" || time.Since(entry.at) < unresolvedTTL {
			return entry.sha, nil
		}
	}

	owner, repo, err := ParseRepoUrl(repoUrl)
	if err != nil {
		return "", err
	}
	pkgOwner, pkgName, ok := strings.Cut(imagePath, "/")
	if !ok || pkgName == "" {
		return "", fmt.Errorf("image path %q: expected <owner>/<package>", imagePath)
	}

	publishedAt, found, err := s.packageVersionTime(ctx, pkgOwner, pkgName, digest)
	if err != nil {
		return "", err
	}

	sha := ""
	if found {
		if sha, err = s.buildRunCommit(ctx, owner, repo, publishedAt); err != nil {
			return "", err
		}
	}

	s.imageCommits.Store(digest, imageCommit{sha: sha, at: time.Now()})
	return sha, nil
}

// packageVersionTime — время публикации версии контейнерного пакета с именем digest.
// Пакет ищется у организации, при 404 — у пользователя с тем же именем.
func (s *Service) packageVersionTime(ctx context.Context, owner, name, digest string) (time.Time, bool, error) {
	opts := &github.PackageListOptions{ListOptions: github.ListOptions{PerPage: perPage}}
	userOwned := false

	for page := 0; page < versionPages; page++ {
		var versions []*github.PackageVersion
		var resp *github.Response
		var err error
		if userOwned {
			versions, resp, err = s.client.Users.PackageGetAllVersions(ctx, owner, "container", name, opts)
		} else {
			versions, resp, err = s.client.Organizations.PackageGetAllVersions(ctx, owner, "container", name, opts)
		}
		if err != nil {
			if isNotFound(resp, err) {
				if !userOwned && page == 0 {
					userOwned = true
					page--
					continue
				}
				return time.Time{}, false, nil
			}
			return time.Time{}, false, fmt.Errorf("PackageGetAllVersions(%s/%s): %w", owner, name, err)
		}

		for _, v := range versions {
			if v.GetName() == digest {
				return v.GetCreatedAt().Time, true, nil
			}
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return time.Time{}, false, nil
}

// buildRunCommit — head_sha push-запуска, внутри которого опубликована версия пакета.
// Несколько подходящих запусков (параллельные ветки) — предпочтение ветке по умолчанию,
// затем запуску, завершившемуся ближе всего к публикации.
func (s *Service) buildRunCommit(ctx context.Context, owner, repo string, publishedAt time.Time) (string, error) {
	runs, _, err := s.client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repo, &github.ListWorkflowRunsOptions{
		Event:       "push",
		Created:     "<=" + publishedAt.UTC().Format(time.RFC3339),
		ListOptions: github.ListOptions{PerPage: runsPerPage},
	})
	if err != nil {
		return "", fmt.Errorf("Actions.ListRepositoryWorkflowRuns(%s/%s): %w", owner, repo, err)
	}

	branch, err := s.defaultBranch(ctx, owner, repo)
	if err != nil {
		return "", err
	}

	return pickBuildRun(runs.WorkflowRuns, publishedAt, branch), nil
}

func pickBuildRun(runs []*github.WorkflowRun, publishedAt time.Time, defaultBranch string) string {
	var best *github.WorkflowRun
	for _, run := range runs {
		started := run.GetRunStartedAt().Time
		if started.IsZero() {
			started = run.GetCreatedAt().Time
		}
		finished := run.GetUpdatedAt().Time
		if publishedAt.Before(started) || publishedAt.After(finished.Add(runSlack)) {
			continue
		}
		if best == nil {
			best = run
			continue
		}
		bestOnDefault, runOnDefault := best.GetHeadBranch() == defaultBranch, run.GetHeadBranch() == defaultBranch
		if runOnDefault != bestOnDefault {
			if runOnDefault {
				best = run
			}
			continue
		}
		if finished.Sub(publishedAt).Abs() < best.GetUpdatedAt().Sub(publishedAt).Abs() {
			best = run
		}
	}
	return best.GetHeadSHA()
}

func isNotFound(resp *github.Response, err error) bool {
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		return true
	}
	ghErr, ok := errors.AsType[*github.ErrorResponse](err)
	return ok && ghErr.Response != nil && ghErr.Response.StatusCode == http.StatusNotFound
}
