package indexer

import "context"

// Indexer — фоновый обход кластера: строит каталог сервисов и workloads.
type Indexer interface {
	// Run выполняет один цикл индексации.
	Run(ctx context.Context) error
	// Start запускает периодический обход; Wait ждёт остановки после отмены ctx.
	Start(ctx context.Context)
	Wait()
}
