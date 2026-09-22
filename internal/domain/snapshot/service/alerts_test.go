package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAlertOwner_ExporterLabels(t *testing.T) {
	s := New(Config{})
	names := []string{"prometheus-kube-state-metrics", "loom", "report", "caravan"}

	// kube-state-metrics: service/job/pod/container — экспортера, объект — job_name
	jobFailed := map[string]string{"alertname": "KubeJobFailed", "namespace": "loom", "job_name": "report-28371", "job": "kube-state-metrics",
		"container": "kube-state-metrics", "pod": "prometheus-kube-state-metrics-1", "service": "prometheus-kube-state-metrics", "alertstate": "firing", "__name__": "ALERTS"}
	owner, ok := s.AlertOwner(jobFailed, names)
	assert.True(t, ok)
	assert.Equal(t, "report", owner, "Job CronJob'а — по префиксу job_name, а не сервис экспортера")
	assert.Equal(t, map[string]string{"alertname": "KubeJobFailed", "namespace": "loom", "job_name": "report-28371", "job": "kube-state-metrics"}, s.AlertLabels(jobFailed))

	// метрика пода (honor_labels): pod/container — объекта, service/job — экспортера
	crash := map[string]string{"alertname": "KubePodCrashLooping", "namespace": "default", "pod": "caravan-7d9f-x1", "container": "caravan",
		"job": "kube-state-metrics", "service": "prometheus-kube-state-metrics"}
	owner, ok = s.AlertOwner(crash, names)
	assert.True(t, ok)
	assert.Equal(t, "caravan", owner)
	assert.Equal(t, map[string]string{"alertname": "KubePodCrashLooping", "namespace": "default", "pod": "caravan-7d9f-x1", "container": "caravan", "job": "kube-state-metrics"}, s.AlertLabels(crash))

	// приложение со своим ServiceMonitor: job == service == container — сам объект
	own := map[string]string{"alertname": "HighErrorRate", "job": "caravan", "service": "caravan", "container": "caravan", "pod": "caravan-1"}
	owner, ok = s.AlertOwner(own, names)
	assert.True(t, ok)
	assert.Equal(t, "caravan", owner)

	_, ok = s.AlertOwner(map[string]string{"alertname": "KubeJobFailed", "job_name": "unknown-1", "job": "kube-state-metrics", "service": "prometheus-kube-state-metrics"}, append(names, "kube-state-metrics"))
	assert.False(t, ok, "без объекта из каталога алерт экспортеру не приписывается, даже если job совпал с именем")

	assert.True(t, s.IsMonitoringAlert(map[string]string{"alertname": "Watchdog", "severity": "none"}))
	assert.False(t, s.IsMonitoringAlert(map[string]string{"alertname": "KubeJobFailed", "severity": "warning"}))
}

func TestMergeAlertLabels(t *testing.T) {
	s := New(Config{})
	assert.Nil(t, s.MergeAlertLabels(nil))

	merged := s.MergeAlertLabels([]map[string]string{
		{"alertname": "KubeJobFailed", "namespace": "loom", "job_name": "b"},
		{"alertname": "KubeJobFailed", "namespace": "loom", "job_name": "a"},
		{"alertname": "KubeJobFailed", "namespace": "loom", "job_name": "a"},
	})
	assert.Equal(t, map[string]string{"alertname": "KubeJobFailed", "namespace": "loom", "job_name": "a, b"}, merged)

	many := make([]map[string]string, 0, 8)
	for _, name := range []string{"h", "g", "f", "e", "d", "c", "b", "a"} {
		many = append(many, map[string]string{"job_name": name})
	}
	assert.Equal(t, "a, b, c, d, e (+3)", s.MergeAlertLabels(many)["job_name"])
}
