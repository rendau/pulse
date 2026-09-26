package indexer

import (
	"context"

	indexerModel "github.com/rendau/pulse/internal/service/indexer/model"
)

// Indexer — фоновый обход кластера: строит каталог сервисов и workloads.
type Indexer interface {
	// Run выполняет один цикл индексации.
	Run(ctx context.Context) error
	// Start запускает периодический обход; Wait ждёт остановки после отмены ctx.
	Start(ctx context.Context)
	Wait()
	// LastCycle — последний цикл; nil — ещё не было
	LastCycle() *indexerModel.Cycle
}
