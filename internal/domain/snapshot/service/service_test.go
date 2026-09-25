package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/mechta-market/pulse/internal/constant"
	"github.com/mechta-market/pulse/internal/domain/snapshot/model"
)

func TestComputeHealth(t *testing.T) {
	s := New(Config{})
	deploy := func(ready int, desired int32, problems ...model.PodProblem) model.WorkloadState {
		return model.WorkloadState{Kind: constant.WorkloadKindDeployment, Name: "api", ReplicasDesired: desired,
			Pods: model.PodsState{Ready: ready, Total: int(desired), Problems: problems}}
	}

	assert.Equal(t, model.HealthUnknown, s.ComputeHealth(&model.Snapshot{}, true))
	assert.Equal(t, model.HealthHealthy, s.ComputeHealth(&model.Snapshot{Workloads: []model.WorkloadState{deploy(3, 3)}}, false))
	assert.Equal(t, model.HealthDown, s.ComputeHealth(&model.Snapshot{Workloads: []model.WorkloadState{deploy(0, 3)}}, false))
	assert.Equal(t, model.HealthDegraded, s.ComputeHealth(&model.Snapshot{Workloads: []model.WorkloadState{deploy(2, 3)}}, false))
	assert.Equal(t, model.HealthDegraded, s.ComputeHealth(&model.Snapshot{
		Workloads: []model.WorkloadState{deploy(3, 3, model.PodProblem{Pod: "api-1", Reason: "CrashLoopBackOff"})}}, false))
	assert.Equal(t, model.HealthDegraded, s.ComputeHealth(&model.Snapshot{
		Workloads: []model.WorkloadState{deploy(3, 3)},
		Alerts:    []model.Alert{{Name: "HighErrorRate", Severity: "critical", State: "active"}}}, false))
	assert.Equal(t, model.HealthHealthy, s.ComputeHealth(&model.Snapshot{
		Workloads: []model.WorkloadState{deploy(3, 3)},
		Alerts:    []model.Alert{{Name: "Info", Severity: "warning", State: "active"}}}, false))
	assert.Equal(t, model.HealthDegraded, s.ComputeHealth(&model.Snapshot{
		Workloads: []model.WorkloadState{deploy(3, 3)},
		Metrics:   []model.Metric{{Anomaly: true}}}, false))
	// CronJob без подов — не down
	assert.Equal(t, model.HealthHealthy, s.ComputeHealth(&model.Snapshot{
		Workloads: []model.WorkloadState{{Kind: constant.WorkloadKindCronJob, Name: "job"}}}, false))
}

func TestApplyBaseline(t *testing.T) {
	s := New(Config{AnomalyThresholdPct: 30})

	m := model.Metric{MetricDef: model.MetricDef{Id: "success_rate", Direction: constant.MetricDirectionHigherIsBetter},
		Current: new(0.31), HourAgo: new(0.98), SameTimeYesterday: new(0.97)}
	s.ApplyBaseline(&m)
	assert.InDelta(t, -68.0, *m.DeltaVsYesterday, 0.1)
	assert.True(t, m.Anomaly)

	// рост «хорошей» метрики — не аномалия
	m = model.Metric{MetricDef: model.MetricDef{Direction: constant.MetricDirectionHigherIsBetter}, Current: new(2.0), SameTimeYesterday: new(1.0)}
	s.ApplyBaseline(&m)
	assert.False(t, m.Anomaly)

	// без direction — любое отклонение
	m = model.Metric{Current: new(2.0), SameTimeYesterday: new(1.0)}
	s.ApplyBaseline(&m)
	assert.True(t, m.Anomaly)

	// нет вчерашнего значения — дельта не определена
	m = model.Metric{Current: new(2.0)}
	s.ApplyBaseline(&m)
	assert.Nil(t, m.DeltaVsYesterday)
	assert.False(t, m.Anomaly)

	// база ноль
	m = model.Metric{Current: new(5.0), SameTimeYesterday: new(0.0)}
	s.ApplyBaseline(&m)
	assert.Nil(t, m.DeltaVsYesterday)
}

func TestSummaryHints(t *testing.T) {
	s := New(Config{})
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	snap := &model.Snapshot{
		Window: time.Hour,
		Alerts: []model.Alert{{Name: "PaymentsDown", Severity: "critical", State: "active", StartsAt: now.Add(-10 * time.Minute), Summary: "success rate < 50%"}},
		Workloads: []model.WorkloadState{{Kind: constant.WorkloadKindDeployment, Namespace: "prod", Name: "payments-api", ReplicasDesired: 6,
			Pods: model.PodsState{Ready: 3, Total: 6, NewestStartedAt: now.Add(-24 * time.Minute),
				Problems: []model.PodProblem{{Pod: "payments-api-1", Reason: "CrashLoopBackOff"}, {Pod: "payments-api-2", Reason: "CrashLoopBackOff"}}}}},
		Metrics: []model.Metric{{MetricDef: model.MetricDef{Id: "success_rate"}, Current: new(0.31), SameTimeYesterday: new(0.97), DeltaVsYesterday: new(-68.0), Anomaly: true}},
		Errors:  []model.SourceError{{Source: "loki"}, {Source: "prometheus", Message: "query: context deadline exceeded"}},
	}

	hints := s.SummaryHints(snap, now)
	assert.Contains(t, hints[0], "PaymentsDown")
	assert.Contains(t, hints, "Deployment prod/payments-api: готово 3 из 6 подов")
	assert.Contains(t, hints, "payments-api: 2 контейнер(ов) в CrashLoopBackOff (payments-api-1, payments-api-2)")
	assert.Contains(t, hints, "payments-api: самый свежий под запущен 24 мин назад (выкатка или рестарт)")
	assert.Contains(t, hints, "success_rate упал на 68% относительно вчера (0.97 → 0.31)")
	assert.Contains(t, hints, "источник loki недоступен: часть картины отсутствует")
	assert.Contains(t, hints, "источник prometheus не успел ответить: часть картины отсутствует")
}
