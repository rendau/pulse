package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	indexerModel "github.com/rendau/pulse/internal/service/indexer/model"
)

// Service — индексер топологии: периодически обходит кластер, сопоставляет образы
// с репозиториями, читает service.yaml и пишет каталог. Хранит только топологию
// и метаданные (Р4); состояние подов и метрики запрашиваются живьём.
type Service struct {
	conf indexerModel.Config

	k8s      k8sClientI
	github   githubClientI
	registry registryClientI
	svc      svcServiceI
	workload workloadServiceI
	deploy   deployServiceI
	depend   dependencyServiceI
	ruto     RutoI
	pods     PodGetterI
	prom     PrometheusI

	mapper *imageMapper
	wg     sync.WaitGroup

	lastMu sync.Mutex
	last   *indexerModel.Cycle
}

func New(
	conf indexerModel.Config,
	k8s k8sClientI,
	github githubClientI,
	registry registryClientI,
	svc svcServiceI,
	workload workloadServiceI,
	deploy deployServiceI,
	depend dependencyServiceI,
	ruto RutoI,
	pods PodGetterI,
	prom PrometheusI,
) *Service {
	return &Service{
		conf:     conf,
		k8s:      k8s,
		github:   github,
		registry: registry,
		svc:      svc,
		workload: workload,
		deploy:   deploy,
		depend:   depend,
		ruto:     ruto,
		pods:     pods,
		prom:     prom,
		mapper:   newImageMapper(conf.ImageMapping),
	}
}

// Start запускает обход сразу и затем по интервалу до отмены ctx.
func (s *Service) Start(ctx context.Context) {
	s.wg.Go(func() {
		s.runLogged(ctx)

		ticker := time.NewTicker(s.conf.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.runLogged(ctx)
			}
		}
	})
}

func (s *Service) Wait() {
	s.wg.Wait()
}

func (s *Service) runLogged(ctx context.Context) {
	if err := s.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("indexer cycle failed", "error", err)
		s.lastMu.Lock()
		s.last = &indexerModel.Cycle{FinishedAt: time.Now(), Error: "цикл не завершился: ошибка каталога или кластера (подробности — в логе pulse)"}
		s.lastMu.Unlock()
	}
}

// LastCycle — последний цикл индексера; nil — ещё не было.
func (s *Service) LastCycle() *indexerModel.Cycle {
	s.lastMu.Lock()
	defer s.lastMu.Unlock()
	return s.last
}
