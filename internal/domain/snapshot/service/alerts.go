package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
)

// alertObjectKeys — лейблы, по значению которых алерт привязывается к сервису/workload'у.
// pod и job_name сравниваются по префиксу: под Deployment'а, Job CronJob'а.
var alertObjectKeys = []string{"service", "app", "job", "deployment", "statefulset", "daemonset", "cronjob", "job_name", "container", "pod", "workload"}

// noisyAlertLabels — служебные лейблы Prometheus/экспортеров, бесполезные модели и раздувающие ответ.
var noisyAlertLabels = []string{"__name__", "alertstate", "condition", "prometheus", "endpoint", "instance", "metrics_path", "uid", "container_id", "image_id"}

// exporterJobs — scrape-job'ы экспортеров: их лейблы job/service (и pod/container, когда
// метрика их не несла и они пришли от цели скрейпа) описывают сам экспортер, а не объект
// алерта. Иначе KubeJobFailed по упавшему Job'у приписывается сервису kube-state-metrics.
// Сам job в лейблах остаётся: для TargetDown это и есть объект.
var exporterJobs = []string{"kube-state-metrics", "node-exporter", "kubelet", "apiserver", "kube-proxy", "kube-scheduler", "kube-controller-manager", "coredns", "etcd"}

// mergedValuesMax — сколько разных значений лейбла перечисляется при слиянии алертов.
const mergedValuesMax = 5

// AlertLabels — лейблы объекта алерта: без служебных и без лейблов экспортера.
func (s *Service) AlertLabels(labels map[string]string) map[string]string {
	result := lo.OmitByKeys(labels, noisyAlertLabels)
	job := labels["job"]
	if !lo.Contains(exporterJobs, job) {
		return result
	}
	delete(result, "service")
	if labels["container"] == job {
		delete(result, "container")
		delete(result, "pod")
	}
	return result
}

// AlertOwner — имя сервиса/workload'а из names, к которому относится алерт по лейблам объекта.
func (s *Service) AlertOwner(labels map[string]string, names []string) (string, bool) {
	labels = s.AlertLabels(labels)
	for _, key := range alertObjectKeys {
		value, ok := labels[key]
		if !ok || value == "" || (key == "job" && lo.Contains(exporterJobs, value)) {
			continue
		}
		for _, name := range names {
			if value == name || ((key == "pod" || key == "job_name") && strings.HasPrefix(value, name+"-")) {
				return name, true
			}
		}
	}
	return "", false
}

// AlertMatches — алерт относится к одному из сервисов/workload'ов names.
func (s *Service) AlertMatches(labels map[string]string, names []string) bool {
	_, ok := s.AlertOwner(labels, names)
	return ok
}

// IsMonitoringAlert — служебный алерт проверки самого мониторинга, а не проблема.
func (s *Service) IsMonitoringAlert(labels map[string]string) bool {
	return labels["alertname"] == "Watchdog" || labels["alertname"] == "InfoInhibitor" || labels["severity"] == "none"
}

// MergeAlertLabels сливает лейблы повторов одного алерта: одинаковые значения остаются,
// различающиеся перечисляются через запятую (до mergedValuesMax, остаток — «(+N)»).
func (s *Service) MergeAlertLabels(all []map[string]string) map[string]string {
	if len(all) == 0 {
		return nil
	}
	values := make(map[string][]string, len(all[0]))
	for _, labels := range all {
		for key, value := range labels {
			if !lo.Contains(values[key], value) {
				values[key] = append(values[key], value)
			}
		}
	}
	return lo.MapValues(values, func(list []string, _ string) string {
		if len(list) == 1 {
			return list[0]
		}
		sort.Strings(list)
		if len(list) <= mergedValuesMax {
			return strings.Join(list, ", ")
		}
		return fmt.Sprintf("%s (+%d)", strings.Join(list[:mergedValuesMax], ", "), len(list)-mergedValuesMax)
	})
}

// AlertSeverity приводит severity алерта к шкале событий.
func (s *Service) AlertSeverity(severity string) string {
	switch strings.ToLower(severity) {
	case "critical", "page", "error":
		return constant.SeverityCritical
	case "warning", "warn":
		return constant.SeverityWarning
	default:
		return constant.SeverityInfo
	}
}
