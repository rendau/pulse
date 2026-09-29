package service

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/labels"

	k8sModel "github.com/rendau/pulse/internal/service/k8s/model"
	svcproxyModel "github.com/rendau/pulse/internal/service/svcproxy/model"
)

// Свои Service — те, что ведут на под самого pulse (его манифест, ручка состояния и ручки).
// Запрос из пода через VIP своего Service обратно в этот же под (hairpin) проходит не во всех
// сетях: без hairpin на мосту узла соединение висит до таймаута (так в yc-zeon: kube-proxy
// iptables без Calico/Cilium). Поэтому свой Service pulse вызывает через localhost — на тот
// порт пода, куда ведёт порт Service (targetPort).

const (
	// карта своих Service: свой под за жизнь процесса не меняется, Service — только с чартом
	selfRoutesTtl = 10 * time.Minute
	// свой под или Service не прочитались — повтор раньше, до тех пор — прошлая карта
	selfRoutesRetry = time.Minute
)

// selfRoutes — свои Service: «service:port» → порт в своём поде.
type selfRoutes struct {
	namespace string
	ports     map[string]int
	expires   time.Time
}

// localPort — порт в своём поде, если target — свой Service. На каждый вызов — только поиск по
// карте; карта перечитывается раз в selfRoutesTtl.
func (s *Service) localPort(ctx context.Context, target svcproxyModel.ServiceTarget) (int, bool) {
	if s.k8s == nil {
		return 0, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.now().Before(s.routes.expires) {
		s.routes = s.loadRoutes(ctx)
	}
	if target.Namespace != s.routes.namespace {
		return 0, false
	}
	port, ok := s.routes.ports[routeKey(target.Service, target.Port)]
	return port, ok
}

// loadRoutes читает свой под и Service его namespace: два запроса к API.
func (s *Service) loadRoutes(ctx context.Context) selfRoutes {
	now := s.now()
	// не прочиталось — прошлая карта, повтор через selfRoutesRetry
	keep := selfRoutes{namespace: s.routes.namespace, ports: s.routes.ports, expires: now.Add(selfRoutesRetry)}

	pod, err := s.k8s.SelfPod(ctx)
	if err != nil {
		slog.Warn("svcproxy: own pod is unknown, own Services are called through their address", "error", err)
		return keep
	}
	if pod == nil {
		// вне кластера своих Service нет
		return selfRoutes{expires: now.Add(selfRoutesTtl)}
	}
	services, err := s.k8s.ListServices(ctx, pod.Namespace)
	if err != nil {
		slog.Warn("svcproxy: services are unavailable, own Services are called through their address", "error", err)
		return keep
	}

	routes := selfRoutes{namespace: pod.Namespace, ports: selfPorts(pod, services), expires: now.Add(selfRoutesTtl)}
	if !maps.Equal(routes.ports, s.routes.ports) {
		slog.Info("svcproxy: own Services are called via localhost", "pod", pod.Namespace+"/"+pod.Name, "routes", describeRoutes(routes.ports))
	}
	return routes
}

// selfPorts — порты Service, которые ведут на под pod: «service:port» → порт в поде.
func selfPorts(pod *k8sModel.Pod, services []k8sModel.Service) map[string]int {
	own := lo.Filter(services, func(svc k8sModel.Service, _ int) bool {
		return svc.Namespace == pod.Namespace && len(svc.Selector) > 0 &&
			labels.SelectorFromSet(svc.Selector).Matches(labels.Set(pod.Labels))
	})
	return lo.FromEntries(lo.FlatMap(own, func(svc k8sModel.Service, _ int) []lo.Entry[string, int] {
		return lo.FilterMap(svc.Ports, func(p k8sModel.ServicePort, _ int) (lo.Entry[string, int], bool) {
			port, ok := targetPort(pod, p)
			return lo.Entry[string, int]{Key: routeKey(svc.Name, int(p.Port)), Value: port}, ok
		})
	}))
}

// targetPort — порт в поде, куда ведёт порт Service: число — как есть, имя — порт контейнера
// пода с этим именем; не задан — тот же, что у Service.
func targetPort(pod *k8sModel.Pod, p k8sModel.ServicePort) (int, bool) {
	if p.TargetPort == "" || p.TargetPort == "0" {
		return int(p.Port), true
	}
	if port, err := strconv.Atoi(p.TargetPort); err == nil {
		return port, true
	}
	named, ok := lo.Find(pod.Ports, func(pp k8sModel.PodPort) bool { return pp.Name == p.TargetPort })
	return int(named.Port), ok
}

func routeKey(service string, port int) string {
	return service + ":" + strconv.Itoa(port)
}

// describeRoutes — карта для лога: «pulse:3003→3003».
func describeRoutes(ports map[string]int) []string {
	result := lo.MapToSlice(ports, func(key string, port int) string { return fmt.Sprintf("%s→%d", key, port) })
	slices.Sort(result)
	return result
}
