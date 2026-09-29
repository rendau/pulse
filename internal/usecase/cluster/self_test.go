package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clusterService "github.com/rendau/pulse/internal/domain/cluster/service"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
	snapshotService "github.com/rendau/pulse/internal/domain/snapshot/service"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
)

type manifestWorkloads struct{}

func (manifestWorkloads) List(context.Context, *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	manifest := workloadModel.Manifest{Status: workloadModel.ManifestOk, Service: "svc", Port: 3003}
	return []*workloadModel.Main{
		{Namespace: "default", Name: "caravan", ServiceName: "caravan", Selector: "app=caravan", Manifest: manifest},
		{Namespace: "default", Name: "pulse", ServiceName: "pulse", Selector: "app=pulse", Manifest: manifest},
		{Namespace: "default", Name: "legacy", ServiceName: "legacy"},
	}, 3, nil
}

type fakeCatalog struct{ names []string }

func (f *fakeCatalog) List(_ context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error) {
	f.names = pars.Names
	var items []*svcModel.Main
	for _, n := range pars.Names {
		items = append(items, &svcModel.Main{Name: n})
	}
	return items, int64(len(items)), nil
}

type fakeSelfReport struct{}

func (fakeSelfReport) Report(_ context.Context, s *svcModel.Main, _ []*workloadModel.Main) (*snapshotModel.SelfReport, []snapshotModel.SourceError) {
	if s.Name == "caravan" {
		return &snapshotModel.SelfReport{Status: "degraded", CheckedAt: time.Now(), Dependencies: []snapshotModel.SelfDependency{
			{Id: "onec", Kind: "grpc", Target: "onec-proxy", Status: "down", Affects: "уведомления 1С"},
		}}, nil
	}
	return &snapshotModel.SelfReport{Status: "ok", CheckedAt: time.Now()}, nil
}

func TestSelfReported(t *testing.T) {
	catalog := &fakeCatalog{}
	u := New(Config{Deadline: 2 * time.Second}, catalog, fakeSelfReport{}, manifestWorkloads{}, &fakeK8s{}, nil, nil, nil, nil, nil,
		clusterService.New(clusterService.Config{}), snapshotService.New(snapshotService.Config{}))

	h, err := u.Health(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"caravan", "pulse"}, catalog.names, "только сервисы с манифестом")
	require.Len(t, h.SelfReported, 1, "ok — не показываем")
	assert.Equal(t, "caravan", h.SelfReported[0].Service)
	assert.Contains(t, h.SelfReported[0].Hints[0], "зависимость onec (grpc → onec-proxy) — down; ломает: уведомления 1С")
	assert.Contains(t, h.SummaryHints, "caravan сообщает о себе: degraded — зависимость onec (grpc → onec-proxy) — down; ломает: уведомления 1С")
}
