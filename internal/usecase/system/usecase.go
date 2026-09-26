package system

import (
	"context"
	"sync"
	"time"

	"github.com/rendau/pulse/internal/constant"
	"github.com/rendau/pulse/internal/usecase/system/model"
)

const pingTimeout = 3 * time.Second

type Usecase struct {
	sources []Source
}

func New(sources []Source) *Usecase {
	return &Usecase{sources: sources}
}

// Ping параллельно опрашивает все источники с общим дедлайном; недоступный источник
// отражается в статусе, а не роняет ответ.
func (u *Usecase) Ping(ctx context.Context) *model.Ping {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	result := &model.Ping{
		Version:     constant.Version,
		GeneratedAt: time.Now().UTC(),
		Sources:     make([]model.SourceStatus, len(u.sources)),
	}

	var wg sync.WaitGroup
	for i, src := range u.sources {
		result.Sources[i] = model.SourceStatus{Name: src.Name, Status: constant.SourceStatusDisabled}
		if src.Ping == nil {
			continue
		}

		wg.Go(func() {
			started := time.Now()
			err := src.Ping(ctx)
			result.Sources[i].Latency = time.Since(started)
			if err != nil {
				result.Sources[i].Status = constant.SourceStatusError
				result.Sources[i].Error = err.Error()
				return
			}
			result.Sources[i].Status = constant.SourceStatusOk
		})
	}
	wg.Wait()

	return result
}
