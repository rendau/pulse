package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStatusImage(t *testing.T) {
	const id = "ghcr.io/mechta-market/airflow-dags/dags@sha256:219acc66d359bc23a8fdc4494c3d299e73d403d5c0074705c6b5cf2716d1f464"

	// обычный случай: имя образа в статусе есть
	assert.Equal(t, "ghcr.io/org/app:latest", statusImage("ghcr.io/org/app:latest", id))
	// образ по digest'у: в статусе только sha256 — имя из imageID
	assert.Equal(t, id, statusImage("sha256:60631816bd06", id))
	assert.Equal(t, id, statusImage("sha256:60631816bd06", "docker-pullable://"+id))
	// imageID без имени — оставляем как есть
	assert.Equal(t, "sha256:60631816bd06", statusImage("sha256:60631816bd06", "sha256:60631816bd06"))
}
