package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/domain/event/model"
)

func TestLinkSyncs(t *testing.T) {
	now := time.Now()
	rollout := model.Rollout{TS: now, Cause: model.RolloutCauseConfigReload, ConfigKind: "secret", ConfigName: "kusec-a-main"}
	object := []model.ConfigSyncObject{{ObjectKind: "secret", ObjectName: "kusec-a-main", Op: "updated"}}

	syncs := []model.ConfigSync{
		{RunId: "too-early", TS: now.Add(-20 * time.Minute), Objects: object},
		{RunId: "far", TS: now.Add(-10 * time.Minute), Objects: object},
		{RunId: "near", TS: now.Add(-1 * time.Minute), Objects: object},
		{RunId: "other-object", TS: now, Objects: []model.ConfigSyncObject{{ObjectKind: "configmap", ObjectName: "kusec-a-main", Op: "updated"}}},
	}

	linked, unlinked := New().LinkSyncs([]model.Rollout{rollout, {TS: now, Cause: model.RolloutCauseRestart}}, syncs)
	require.Len(t, linked, 2)
	require.NotNil(t, linked[0].Sync)
	assert.Equal(t, "near", linked[0].Sync.RunId, "ближайший sync того же объекта в окне")
	assert.Nil(t, linked[1].Sync, "ручной рестарт не связывается")
	assert.ElementsMatch(t, []string{"too-early", "far", "other-object"}, []string{unlinked[0].RunId, unlinked[1].RunId, unlinked[2].RunId})

	event := New().FromRollout(linked[0], "a")
	assert.Equal(t, "near", event.Details["sync_run_id"])

	failed := New().FromConfigSync(model.ConfigSync{RunId: "x", Status: "error", Error: "forbidden", Objects: object}, "a")
	assert.Equal(t, "warning", failed.Severity)
	assert.Contains(t, failed.Summary, "forbidden")
}
