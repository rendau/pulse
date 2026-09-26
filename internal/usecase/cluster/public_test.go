package cluster

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/constant"
	clusterModel "github.com/rendau/pulse/internal/domain/cluster/model"
	clusterService "github.com/rendau/pulse/internal/domain/cluster/service"
	dependencyModel "github.com/rendau/pulse/internal/domain/dependency/model"
	logsModel "github.com/rendau/pulse/internal/domain/logs/model"
	snapshotService "github.com/rendau/pulse/internal/domain/snapshot/service"
	workloadModel "github.com/rendau/pulse/internal/domain/workload/model"
	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	prometheusModel "github.com/rendau/pulse/internal/service/prometheus/model"
	rutoModel "github.com/rendau/pulse/internal/service/ruto/model"
	rutoService "github.com/rendau/pulse/internal/service/ruto/service"
)

type listWorkloads []*workloadModel.Main

func (l listWorkloads) List(context.Context, *workloadModel.ListReq) ([]*workloadModel.Main, int64, error) {
	return l, int64(len(l)), nil
}

// fakeRuto — снапшот задан, разбор логов — настоящий.
type fakeRuto struct {
	*rutoService.Service
	snapshot *rutoModel.Snapshot
}

func (f fakeRuto) GetSnapshot(context.Context) (*rutoModel.Snapshot, error) { return f.snapshot, nil }

type fakeDepend []*dependencyModel.Main

func (f fakeDepend) List(_ context.Context, pars *dependencyModel.ListReq) ([]*dependencyModel.Main, int64, error) {
	return lo.Filter(f, func(e *dependencyModel.Main, _ int) bool { return lo.Contains(pars.FromServices, e.FromService) }), int64(len(f)), nil
}

// gatewayProm — метрики gateway по виду запроса: сейчас, сутки до окна, окном раньше, вчера.
type gatewayProm map[string][]prometheusModel.Sample

func (p gatewayProm) Query(_ context.Context, promql string, _ time.Time) ([]prometheusModel.Sample, error) {
	key := "now"
	switch {
	case strings.Contains(promql, "[1d] offset"):
		key = "usual"
	case strings.Contains(promql, "offset 1d"):
		key = "yesterday"
	case strings.Contains(promql, "offset"):
		key = "prev"
	}
	if strings.Contains(promql, "histogram_quantile") {
		key = "p95_" + key
	}
	return p[key], nil
}

func sample(app, status string, v float64) prometheusModel.Sample {
	labels := map[string]string{"app": app}
	if status != "" {
		labels["status"] = status
	}
	return prometheusModel.Sample{Labels: labels, Value: v}
}

func TestHealth_PublicApps(t *testing.T) {
	prom := gatewayProm{
		"now": {
			sample("ocenter", "200", 800), sample("ocenter", "502", 200), // 20% сбоев
			sample("fine", "200", 1000), sample("fine", "500", 1),
			sample("slow", "200", 500),
			sample("quiet", "200", 2),
		},
		"usual": {
			sample("ocenter", "200", 99900), sample("ocenter", "500", 100),
			sample("fine", "200", 90000), sample("fine", "500", 90),
		},
		"prev":          {sample("quiet", "", 800), sample("fine", "", 1000)},
		"yesterday":     {sample("quiet", "", 900), sample("fine", "", 950)},
		"p95_now":       {sample("slow", "", 3.5), sample("fine", "", 0.2)},
		"p95_yesterday": {sample("slow", "", 0.4), sample("fine", "", 0.2)},
	}
	logs := &fakeLogs{lines: []logsModel.Line{
		{Text: `{"level":"ERROR","msg":"proxy error GET /delivery/x","reason":"backend connection refused","error":"dial tcp 10.0.0.1:80: connect: connection refused","app_name":"delivery"}`},
		{Text: `{"level":"ERROR","msg":"proxy error GET /delivery/x","reason":"backend connection refused","error":"dial tcp","app_name":"delivery"}`},
		{Text: `{"level":"ERROR","msg":"proxy error GET /delivery/x","reason":"backend connection refused","error":"dial tcp","app_name":"delivery"}`},
		{Text: `{"level":"ERROR","msg":"proxy error GET /delivery/y","reason":"backend response timeout","error":"context deadline exceeded","app_name":"delivery"}`},
		{Text: `{"level":"ERROR","msg":"proxy error GET /delivery/y","reason":"backend response timeout","error":"context deadline exceeded","app_name":"delivery"}`},
		{Text: `{"level":"ERROR","msg":"proxy error GET /fine/y","reason":"backend closed connection","error":"EOF","app_name":"fine"}`},
		{Text: `{"level":"ERROR","msg":"request transform: compile failed","error":"SyntaxError: Unexpected token","app_id":"a3","endpoint_id":"e1"}`},
	}}
	ruto := fakeRuto{Service: rutoService.New("http://ruto"), snapshot: &rutoModel.Snapshot{Apps: []rutoModel.App{
		{Id: "a3", Name: "news", Active: true, PathPrefix: "/news", Endpoints: []rutoModel.Endpoint{{Id: "e1", Active: true, Type: "http", Method: "POST", Path: "publish"}}},
	}}}
	edge := func(app, service string) *dependencyModel.Main {
		return &dependencyModel.Main{FromService: "ruto-gateway", ToService: service, Key: app, Source: dependencyModel.SourceRuto}
	}
	depend := fakeDepend{edge("ocenter", "orders-center"), edge("delivery", "delivery"), edge("seller", "seller"), edge("fine", "fine"),
		{FromService: "caravan", ToService: "seller", Key: "SELLER_URL", Source: dependencyModel.SourceEnv}}
	workloads := listWorkloads{
		{Namespace: "default", Kind: constant.WorkloadKindDeployment, Name: "seller", ServiceName: "seller", ReplicasDesired: 2},
		{Namespace: "default", Kind: constant.WorkloadKindDeployment, Name: "fine", ServiceName: "fine", ReplicasDesired: 1},
		{Namespace: "default", Kind: constant.WorkloadKindDeployment, Name: "ruto-gateway", ServiceName: "ruto-gateway", ReplicasDesired: 1},
	}
	k8s := &fakeK8s{nodes: []k8sModel.Node{{Name: "n1", Ready: true}}, pods: []k8sModel.Pod{
		{Namespace: "default", Name: "seller-7d9f8b6c4d-x2k4p", Phase: "Running", Ready: false},
		{Namespace: "default", Name: "fine-7d9f8b6c4d-x2k4p", Phase: "Running", Ready: true},
	}}

	u := New(Config{Deadline: 2 * time.Second, Public: PublicConfig{GatewayService: "ruto-gateway", RequestsMetric: "gw_requests_total", DurationMetric: "gw_duration_seconds"}},
		nil, nil, workloads, k8s, prom, nil, logs, ruto, depend,
		clusterService.New(clusterService.Config{}), snapshotService.New(snapshotService.Config{}))

	h, err := u.Health(context.Background(), 15*time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "ruto-gateway", logs.service)
	assert.Equal(t, ruto.GatewayErrorsFilter(), logs.pattern)

	byApp := lo.SliceToMap(h.PublicApps, func(a clusterModel.PublicApp) (string, clusterModel.PublicApp) { return a.App, a })
	assert.ElementsMatch(t, []string{"ocenter", "delivery", "seller", "news", "slow", "quiet"}, lo.Keys(byApp), "fine — в норме")
	kinds := func(app string) []string {
		return lo.Map(byApp[app].Problems, func(p clusterModel.PublicProblem, _ int) string { return p.Kind })
	}

	assert.Equal(t, []string{clusterModel.PublicErrors}, kinds("ocenter"))
	assert.Equal(t, "orders-center", byApp["ocenter"].Service)
	assert.Contains(t, byApp["ocenter"].Problems[0].Text, "20%")

	assert.Equal(t, []string{clusterModel.PublicBackend}, kinds("delivery"))
	assert.Equal(t, []clusterModel.GatewayReason{
		{Reason: "backend connection refused", Count: 3, Example: "dial tcp 10.0.0.1:80: connect: connection refused"},
		{Reason: "backend response timeout", Count: 2, Example: "context deadline exceeded"},
	}, byApp["delivery"].BackendErrors)

	assert.Equal(t, []string{clusterModel.PublicBackend}, kinds("seller"))
	assert.Equal(t, &clusterModel.PodsReady{Ready: 0, Desired: 2}, byApp["seller"].BackendPods)
	assert.Contains(t, byApp["seller"].Problems[0].Text, "нет готовых подов")

	assert.Equal(t, []string{clusterModel.PublicScript}, kinds("news"))
	assert.Equal(t, []clusterModel.ScriptError{{Route: "POST /news/publish", Reason: "request transform: compile failed", Count: 1, Example: "SyntaxError: Unexpected token"}},
		byApp["news"].ScriptErrors)

	assert.Equal(t, []string{clusterModel.PublicSlow}, kinds("slow"))
	assert.Equal(t, []string{clusterModel.PublicNoTraffic}, kinds("quiet"))

	assert.True(t, lo.ContainsBy(h.SummaryHints, func(s string) bool {
		return strings.HasPrefix(s, "публичное приложение ocenter (orders-center): сбои")
	}))
}

func TestHealth_PublicDisabled(t *testing.T) {
	logs := &fakeLogs{}
	u := New(Config{Deadline: 2 * time.Second}, nil, nil, fakeWorkload{}, &fakeK8s{}, gatewayProm{}, nil, logs, nil, nil,
		clusterService.New(clusterService.Config{}), snapshotService.New(snapshotService.Config{}))
	h, err := u.Health(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.Empty(t, h.PublicApps)
	assert.Empty(t, logs.service, "без ruto логи gateway не читаются")
}
