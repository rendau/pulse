package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/constant"
	"github.com/rendau/pulse/internal/domain/event/model"
)

func reloaded(name, hash string) string {
	return `{"type":"CONFIGMAP","name":"` + name + `","namespace":"default","hash":"` + hash + `","containerRefs":["app"],"observedAt":1785488850}`
}

func TestRollouts(t *testing.T) {
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	keel := func(at time.Time) string { return at.String() }

	revisions := []model.PodTemplateRevision{
		// порядок перемешан: сортировка по ревизии
		{Name: "caravan-4", Revision: 4, CreatedAt: now.Add(-1 * time.Hour), Annotations: map[string]string{
			reloaderAnnotation: reloaded("kusec-caravan-main", "bbbbbbbbbbbbbbbbbbbb"), restartAnnotation: "2026-09-17T10:00:00Z", "keel.sh/update-time": keel(now.Add(-3 * time.Hour)),
		}},
		{Name: "caravan-1", Revision: 1, CreatedAt: now.Add(-48 * time.Hour), Annotations: map[string]string{
			reloaderAnnotation: reloaded("kusec-caravan-main", "aaaaaaaaaaaaaaaaaaaa"),
		}},
		{Name: "caravan-2", Revision: 2, CreatedAt: now.Add(-5 * time.Hour), Annotations: map[string]string{
			reloaderAnnotation: reloaded("kusec-caravan-main", "aaaaaaaaaaaaaaaaaaaa"), "keel.sh/update-time": keel(now.Add(-5 * time.Hour)),
		}},
		{Name: "caravan-3", Revision: 3, CreatedAt: now.Add(-3 * time.Hour), Annotations: map[string]string{
			reloaderAnnotation: reloaded("kusec-caravan-main", "bbbbbbbbbbbbbbbbbbbb"), "keel.sh/update-time": keel(now.Add(-3 * time.Hour)),
		}},
	}

	rollouts := New().Rollouts("default/caravan", revisions, since)
	require.Len(t, rollouts, 2, "ревизия 2 — новый образ (keel), не конфигурация")

	assert.Equal(t, model.RolloutCauseConfigReload, rollouts[0].Cause)
	assert.Equal(t, int64(3), rollouts[0].Revision)
	assert.Equal(t, "configmap", rollouts[0].ConfigKind)
	assert.Equal(t, "kusec-caravan-main", rollouts[0].ConfigName)
	assert.Equal(t, "bbbbbbbbbbbb", rollouts[0].Hash)
	assert.Equal(t, "aaaaaaaaaaaa", rollouts[0].PrevHash)

	assert.Equal(t, model.RolloutCauseRestart, rollouts[1].Cause)
	assert.Equal(t, int64(4), rollouts[1].Revision)

	event := New().FromRollout(rollouts[0], "caravan")
	assert.Equal(t, constant.EventTypeConfigChange, event.Type)
	assert.Contains(t, event.Summary, "kusec-caravan-main")
}

func TestRollouts_OutsideWindowAndNoPrevious(t *testing.T) {
	now := time.Now()
	revisions := []model.PodTemplateRevision{
		{Revision: 7, CreatedAt: now.Add(-time.Hour), Annotations: map[string]string{reloaderAnnotation: reloaded("cm", "x")}},
	}
	assert.Empty(t, New().Rollouts("default/a", revisions, now.Add(-24*time.Hour)), "без предыдущей ревизии сравнивать не с чем")

	revisions = append(revisions, model.PodTemplateRevision{Revision: 8, CreatedAt: now.Add(-48 * time.Hour), Annotations: map[string]string{reloaderAnnotation: reloaded("cm", "y")}})
	assert.Empty(t, New().Rollouts("default/a", revisions, now.Add(-24*time.Hour)), "выкатка вне окна")
}
