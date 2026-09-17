package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/labels"

	dependencyModel "github.com/mechta-market/pulse/internal/domain/dependency/model"
	k8sModel "github.com/mechta-market/pulse/internal/service/k8s/model"
)

// recordDependencies строит рёбра «сервис → хост» из env контейнеров: inline-значения и
// ссылки на configmap. Значения секретов не читаются (Р7): переменная из secret даёт только
// факт ссылки без адреса и в граф не попадает. Хост резолвится в сервис каталога через
// k8s Service (селектор → workload) или по совпадению имени workload'а в namespace.
func (s *Service) recordDependencies(ctx context.Context, drafts []*workloadDraft, now time.Time) int {
	configMaps, err := s.k8s.ListConfigMaps(ctx, "")
	if err != nil {
		slog.Warn("indexer: configmaps are unavailable, env-from-configmap dependencies skipped", "error", err)
	}
	cmByKey := lo.SliceToMap(configMaps, func(cm k8sModel.ConfigMap) (string, k8sModel.ConfigMap) {
		return cm.Namespace + "/" + cm.Name, cm
	})

	services, err := s.k8s.ListServices(ctx, "")
	if err != nil {
		slog.Warn("indexer: k8s services are unavailable, hosts resolve by workload name only", "error", err)
	}
	resolver := newHostResolver(s.depend.ClusterHost, drafts, services)

	edits := make(map[string]*dependencyModel.Edit, 64)
	for _, d := range drafts {
		for key, value := range envValues(d, cmByKey) {
			for _, ep := range s.depend.ParseEndpoints(value.value) {
				toService := resolver.resolve(ep.Host, d.Namespace)
				if toService == d.serviceKey {
					continue // ссылка на себя (например, свой redis-сайдкар) — не связь
				}
				edit := &dependencyModel.Edit{
					Cluster:     new(s.conf.Cluster),
					FromService: new(d.serviceKey),
					ToService:   new(toService),
					ToHost:      new(ep.Host),
					Port:        new(ep.Port),
					Scheme:      new(ep.Scheme),
					Source:      new(value.source),
					Key:         new(key),
					FirstSeen:   new(now),
					LastSeen:    new(now),
				}
				edits[fmt.Sprintf("%s|%s|%d|%s", d.serviceKey, ep.Host, ep.Port, key)] = edit
			}
		}
	}

	for _, edit := range s.rutoEdits(ctx, drafts, resolver, now) {
		edits[fmt.Sprintf("%s|%s|%d|%s", *edit.FromService, *edit.ToHost, *edit.Port, *edit.Key)] = edit
	}

	if len(edits) == 0 {
		return 0
	}
	if err = s.depend.UpdateOrCreateMany(ctx, lo.Values(edits)); err != nil {
		slog.Warn("indexer: dependencies upsert failed", "error", err)
		return 0
	}
	return len(edits)
}

// rutoEdits — рёбра «gateway ruto → backend приложения» из опубликованной конфигурации
// gateway (ключ ребра — имя приложения ruto). По ним get_public_api и снапшот находят
// приложения ruto сервиса. ruto недоступен — рёбра не обновляются и уходят по stale_after.
func (s *Service) rutoEdits(ctx context.Context, drafts []*workloadDraft, resolver *hostResolver, now time.Time) []*dependencyModel.Edit {
	if s.ruto == nil || s.conf.RutoGatewayService == "" {
		return nil
	}

	snapshot, err := s.ruto.GetSnapshot(ctx)
	if err != nil {
		slog.Warn("indexer: ruto is unavailable, public routes are not updated", "error", err)
		return nil
	}

	// хост backend'а без namespace — в namespace gateway
	namespace := "default"
	if gw, ok := lo.Find(drafts, func(d *workloadDraft) bool { return d.serviceKey == s.conf.RutoGatewayService }); ok {
		namespace = gw.Namespace
	}

	result := make([]*dependencyModel.Edit, 0, len(snapshot.Apps))
	for _, app := range snapshot.Apps {
		if !app.Active {
			continue
		}
		for _, raw := range []string{app.BackendUrl, grpcTarget(app.GrpcUrl)} {
			for _, ep := range s.depend.ParseEndpoints(raw) {
				result = append(result, &dependencyModel.Edit{
					Cluster:     new(s.conf.Cluster),
					FromService: new(s.conf.RutoGatewayService),
					ToService:   new(resolver.resolve(ep.Host, namespace)),
					ToHost:      new(ep.Host),
					Port:        new(ep.Port),
					Scheme:      new(ep.Scheme),
					Source:      new(dependencyModel.SourceRuto),
					Key:         new(app.Name),
					FirstSeen:   new(now),
					LastSeen:    new(now),
				})
			}
		}
	}
	return result
}

// grpcTarget снимает схему резолвера gRPC (dns:///host:port, passthrough:///host:port).
func grpcTarget(target string) string {
	if _, rest, ok := strings.Cut(target, ":///"); ok {
		return rest
	}
	return target
}

type envValue struct {
	value  string
	source string
}

// envValues — переменные всех контейнеров workload'а с раскрытыми ссылками на configmap.
func envValues(d *workloadDraft, cmByKey map[string]k8sModel.ConfigMap) map[string]envValue {
	result := make(map[string]envValue, 32)
	for _, c := range d.Containers {
		for _, cmName := range c.EnvFromConfigMaps {
			if cm, ok := cmByKey[d.Namespace+"/"+cmName]; ok {
				for key, value := range cm.Data {
					result[key] = envValue{value: value, source: dependencyModel.SourceConfigMap}
				}
			}
		}
		for _, env := range c.Env {
			switch {
			case env.FromSecret:
				continue
			case env.ConfigMapRef != "":
				cmName, cmKey, _ := strings.Cut(env.ConfigMapRef, "/")
				if cm, ok := cmByKey[d.Namespace+"/"+cmName]; ok {
					result[env.Name] = envValue{value: cm.Data[cmKey], source: dependencyModel.SourceConfigMap}
				}
			default:
				result[env.Name] = envValue{value: env.Value, source: dependencyModel.SourceEnv}
			}
		}
	}
	return result
}

// hostResolver — хост внутри кластера → сервис каталога.
type hostResolver struct {
	clusterHost func(host string) (string, string, bool)
	// byWorkload: namespace/name → serviceKey
	byWorkload map[string]string
	// byK8sService: namespace/name → serviceKey (через селектор Service → поды workload'а)
	byK8sService map[string]string
}

func newHostResolver(clusterHost func(string) (string, string, bool), drafts []*workloadDraft, services []k8sModel.Service) *hostResolver {
	r := &hostResolver{clusterHost: clusterHost, byWorkload: make(map[string]string, len(drafts)), byK8sService: make(map[string]string, len(services))}
	for _, d := range drafts {
		r.byWorkload[d.Namespace+"/"+d.Name] = d.serviceKey
	}
	for _, svc := range services {
		if len(svc.Selector) == 0 {
			continue
		}
		selector := labels.SelectorFromSet(svc.Selector)
		for _, d := range drafts {
			if d.Namespace != svc.Namespace || d.Selector == "" {
				continue
			}
			// селектор Service — подмножество лейблов подов workload'а: сверяем с его собственным селектором
			podLabels, err := labels.ConvertSelectorToLabelsMap(d.Selector)
			if err != nil {
				continue
			}
			if selector.Matches(podLabels) {
				r.byK8sService[svc.Namespace+"/"+svc.Name] = d.serviceKey
				break
			}
		}
	}
	return r
}

func (r *hostResolver) resolve(host, defaultNamespace string) string {
	name, namespace, ok := r.clusterHost(host)
	if !ok {
		return ""
	}
	if namespace == "" {
		namespace = defaultNamespace
	}
	if service, ok := r.byK8sService[namespace+"/"+name]; ok {
		return service
	}
	if service, ok := r.byWorkload[namespace+"/"+name]; ok {
		return service
	}
	// имя k8s Service с суффиксом компонента (kafka-producer-redis) → сервис kafka-producer
	for key, service := range r.byWorkload {
		ns, wl, _ := strings.Cut(key, "/")
		if ns == namespace && strings.HasPrefix(name, wl+"-") {
			return service
		}
	}
	return ""
}
