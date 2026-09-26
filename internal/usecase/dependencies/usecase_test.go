package dependencies

import (
	"context"
	"fmt"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dependencyModel "github.com/rendau/pulse/internal/domain/dependency/model"
	snapshotModel "github.com/rendau/pulse/internal/domain/snapshot/model"
	snapshotService "github.com/rendau/pulse/internal/domain/snapshot/service"
	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	"github.com/rendau/pulse/internal/usecase/dependencies/model"
)

type fakeSvc struct{}

func (fakeSvc) GetOrSuggest(_ context.Context, name string) (*svcModel.Main, error) {
	return &svcModel.Main{Name: name}, nil
}

func (fakeSvc) List(_ context.Context, pars *svcModel.ListReq) ([]*svcModel.Main, int64, error) {
	return lo.Map(pars.Names, func(n string, _ int) *svcModel.Main { return &svcModel.Main{Name: n, Title: "T:" + n} }), 0, nil
}

type fakeWorkload struct{}

func (fakeWorkload) List(_ context.Context, pars *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	return lo.Map(pars.ServiceNames, func(n string, _ int) *workloadModel.Main {
		return &workloadModel.Main{Namespace: "prod", Kind: "Deployment", Name: n, ServiceName: n, ReplicasDesired: 2, Selector: "app=" + n}
	}), 0, nil
}

type fakeDepend struct{ items []*dependencyModel.Main }

func (f fakeDepend) List(context.Context, *dependencyModel.ListReq) ([]*dependencyModel.Main, int64, error) {
	return f.items, int64(len(f.items)), nil
}

type fakeK8s struct{ down map[string]bool }

func (f fakeK8s) ListPods(_ context.Context, _, selector string) ([]k8sModel.Pod, error) {
	name := selector[len("app="):]
	if f.down[name] {
		return []k8sModel.Pod{{Name: name + "-1", Ready: false, Containers: []k8sModel.PodContainer{{Name: "app", State: "waiting", Reason: "CrashLoopBackOff"}}}}, nil
	}
	return []k8sModel.Pod{{Name: name + "-1", Ready: true}, {Name: name + "-2", Ready: true}}, nil
}

func edge(from, to, host string, port int32, key string) *dependencyModel.Main {
	return &dependencyModel.Main{Cluster: "zeon", FromService: from, ToService: to, ToHost: host, Port: port, Source: "env", Key: key}
}

func newUsecase(items []*dependencyModel.Main, down map[string]bool, maxNodes int) *Usecase {
	return New(Config{MaxNodes: maxNodes, MaxDepth: 3}, fakeSvc{}, fakeWorkload{}, fakeDepend{items}, fakeK8s{down}, snapshotService.New(snapshotService.Config{}))
}

func TestGraph_BothDirectionsWithHealth(t *testing.T) {
	items := []*dependencyModel.Main{
		edge("payments-api", "acquirer-gateway", "acquirer-gateway.prod.svc", 8080, "ACQUIRER_URL"),
		edge("payments-api", "acquirer-gateway", "acquirer-gateway.prod.svc", 8080, "ACQUIRER_BASE"),
		edge("payments-api", "", "api.epay.kz", 443, "EPAY_URL"),
		edge("checkout", "payments-api", "payments-api", 80, "PAYMENTS_URL"),
		edge("acquirer-gateway", "billing-core", "billing-core", 9090, "BILLING_ADDR"),
	}
	u := newUsecase(items, map[string]bool{"acquirer-gateway": true}, 50)

	g, err := u.Graph(context.Background(), &model.GraphReq{Service: "payments-api"})
	require.NoError(t, err)
	assert.Empty(t, g.Errors)
	assert.False(t, g.Truncated)

	names := lo.Map(g.Nodes, func(n model.Node, _ int) string { return n.Name })
	assert.Equal(t, []string{"payments-api", "acquirer-gateway", "checkout", "external:api.epay.kz"}, names, "depth=1: billing-core не входит")

	acq, _ := lo.Find(g.Nodes, func(n model.Node) bool { return n.Name == "acquirer-gateway" })
	assert.Equal(t, snapshotModel.HealthDown, acq.Health, "0 ready при желаемых 2 — down")
	assert.Equal(t, "0/1 ready", acq.PodsInfo)
	assert.Equal(t, "T:acquirer-gateway", acq.Title)

	ext, _ := lo.Find(g.Nodes, func(n model.Node) bool { return n.External })
	assert.Empty(t, ext.Health)

	require.Len(t, g.Edges, 3)
	acqEdge, _ := lo.Find(g.Edges, func(e model.Edge) bool { return e.To == "acquirer-gateway" })
	assert.Equal(t, []string{"ACQUIRER_BASE", "ACQUIRER_URL"}, acqEdge.Keys, "одинаковые адреса из разных ключей — одно ребро")
}

func TestGraph_DirectionAndDepth(t *testing.T) {
	items := []*dependencyModel.Main{
		edge("payments-api", "acquirer-gateway", "acquirer-gateway", 8080, "A"),
		edge("checkout", "payments-api", "payments-api", 80, "B"),
		edge("acquirer-gateway", "billing-core", "billing-core", 9090, "C"),
	}
	u := newUsecase(items, nil, 50)

	g, err := u.Graph(context.Background(), &model.GraphReq{Service: "payments-api", Direction: "upstream", Depth: 2})
	require.NoError(t, err)
	names := lo.Map(g.Nodes, func(n model.Node, _ int) string { return n.Name })
	assert.Equal(t, []string{"payments-api", "acquirer-gateway", "billing-core"}, names)

	g, err = u.Graph(context.Background(), &model.GraphReq{Service: "payments-api", Direction: "downstream"})
	require.NoError(t, err)
	names = lo.Map(g.Nodes, func(n model.Node, _ int) string { return n.Name })
	assert.Equal(t, []string{"payments-api", "checkout"}, names)

	_, err = u.Graph(context.Background(), &model.GraphReq{Service: "payments-api", Direction: "sideways"})
	assert.ErrorContains(t, err, "direction")
	_, err = u.Graph(context.Background(), &model.GraphReq{Service: "payments-api", Depth: 9})
	assert.ErrorContains(t, err, "depth")
}

// критерий фазы 5: depth=2 не приводит к взрывному росту — лимит узлов
func TestGraph_NodeLimit(t *testing.T) {
	items := make([]*dependencyModel.Main, 0, 200)
	for i := 0; i < 40; i++ {
		hub := fmt.Sprintf("hub-%d", i)
		items = append(items, edge("root", hub, hub, 80, "H"))
		for j := 0; j < 5; j++ {
			leaf := fmt.Sprintf("leaf-%d-%d", i, j)
			items = append(items, edge(hub, leaf, leaf, 80, "L"))
		}
	}
	u := newUsecase(items, nil, 50)

	g, err := u.Graph(context.Background(), &model.GraphReq{Service: "root", Depth: 2})
	require.NoError(t, err)
	assert.True(t, g.Truncated)
	assert.LessOrEqual(t, len(g.Nodes), 50)
	for _, e := range g.Edges {
		assert.True(t, lo.ContainsBy(g.Nodes, func(n model.Node) bool { return n.Name == e.To }), "рёбра только между узлами ответа")
	}
}
