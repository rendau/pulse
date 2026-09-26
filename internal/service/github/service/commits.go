package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/go-github/v82/github"
	"github.com/samber/lo"

	githubModel "github.com/rendau/pulse/internal/service/github/model"
)

const perPage = 100

func (s *Service) ListCommits(ctx context.Context, repoUrl string, since, until time.Time, limit int) ([]githubModel.Commit, error) {
	owner, repo, err := ParseRepoUrl(repoUrl)
	if err != nil {
		return nil, err
	}

	branch, err := s.defaultBranch(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	result := make([]githubModel.Commit, 0, 32)
	opts := &github.CommitsListOptions{SHA: branch, Since: since, Until: until, ListOptions: github.ListOptions{PerPage: perPage}}
	for {
		commits, resp, err := s.client.Repositories.ListCommits(ctx, owner, repo, opts)
		if err != nil {
			return nil, fmt.Errorf("Repositories.ListCommits(%s/%s): %w", owner, repo, err)
		}
		result = append(result, lo.Map(commits, encodeCommit)...)
		if len(result) >= limit || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *Service) CompareCommits(ctx context.Context, repoUrl, base string) (*githubModel.Comparison, error) {
	owner, repo, err := ParseRepoUrl(repoUrl)
	if err != nil {
		return nil, err
	}

	branch, err := s.defaultBranch(ctx, owner, repo)
	if err != nil {
		return nil, err
	}

	cmp, _, err := s.client.Repositories.CompareCommits(ctx, owner, repo, base, branch, &github.ListOptions{PerPage: perPage})
	if err != nil {
		return nil, fmt.Errorf("Repositories.CompareCommits(%s/%s, %s...%s): %w", owner, repo, base, branch, err)
	}

	commits := lo.Map(cmp.Commits, encodeCommit)
	// GitHub отдаёт старые первыми; в ответах сервиса — новые первыми
	slices.Reverse(commits)

	return &githubModel.Comparison{
		AheadBy:  cmp.GetAheadBy(),
		BehindBy: cmp.GetBehindBy(),
		Commits:  commits,
	}, nil
}

// defaultBranch — ветка по умолчанию репозитория.
func (s *Service) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	info, err := s.repoInfo(ctx, owner, repo)
	if err != nil {
		return "", err
	}
	return info.DefaultBranch, nil
}

func encodeCommit(v *github.RepositoryCommit, _ int) githubModel.Commit {
	result := githubModel.Commit{
		SHA: v.GetSHA(),
		Url: v.GetHTMLURL(),
	}
	if commit := v.GetCommit(); commit != nil {
		result.Message, _, _ = strings.Cut(strings.TrimSpace(commit.GetMessage()), "\n")
		if author := commit.GetAuthor(); author != nil {
			result.Author = author.GetName()
			result.Date = author.GetDate().Time
		}
	}
	if author := v.GetAuthor(); author != nil {
		result.Login = author.GetLogin()
	}
	if result.Author == "" {
		result.Author = result.Login
	}
	return result
}
