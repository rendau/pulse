package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/mechta-market/pulse/internal/config"
)

func runMigrations() {
	if config.Conf.PgDsn == "" {
		slog.Warn("PG-dsn is empty, migrations will not be applied")
		return
	}

	absPath, _ := filepath.Abs("./migrations")

	// check migrations folder exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		slog.Warn("migrations folder does not exist, migrations will not be applied", "path", absPath)
		return
	}

	m, err := migrate.New("file://"+absPath, config.Conf.PgDsn)
	if err != nil {
		panic(err)
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		panic(fmt.Errorf("migration up error: %w", err))
	}
}
