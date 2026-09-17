package service

import (
	"testing"
	"time"

	"github.com/google/go-github/v82/github"
	"github.com/stretchr/testify/assert"
)

func run(sha, branch string, started, finished time.Time) *github.WorkflowRun {
	return &github.WorkflowRun{
		HeadSHA: new(sha), HeadBranch: new(branch),
		RunStartedAt: &github.Timestamp{Time: started}, UpdatedAt: &github.Timestamp{Time: finished},
	}
}

func TestPickBuildRun(t *testing.T) {
	published := time.Date(2026, 9, 17, 4, 30, 0, 0, time.UTC)
	at := func(m int) time.Time { return published.Add(time.Duration(m) * time.Minute) }

	runs := []*github.WorkflowRun{
		run("later", "master", at(5), at(9)),
		run("develop", "develop", at(-4), at(1)),
		run("master", "master", at(-6), at(1)),
		run("earlier", "master", at(-30), at(-20)),
	}
	assert.Equal(t, "master", pickBuildRun(runs, published, "master"), "параллельная сборка ветки develop уступает ветке по умолчанию")

	// публикация чуть позже завершения запуска (updated_at) — в пределах допуска
	assert.Equal(t, "earlier", pickBuildRun([]*github.WorkflowRun{run("earlier", "master", at(-30), at(-1))}, published, "master"))

	assert.Empty(t, pickBuildRun([]*github.WorkflowRun{run("x", "master", at(-30), at(-20))}, published, "master"),
		"ни один запуск не покрывает публикацию — коммит не угадывается")
}
