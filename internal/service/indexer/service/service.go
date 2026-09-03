package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	indexerModel "github.com/mechta-market/pulse/internal/service/indexer/model"
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

	mapper *imageMapper
	wg     sync.WaitGroup
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
	}
}
