package mcp

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/mechta-market/pulse/internal/errs"
)

// toolError переводит ошибку usecase в текст для модели: семантические ошибки
// (неизвестный сервис, неверный параметр) отдаются как есть с описанием, чтобы агент
// мог исправиться; внутренние — обезличенно, с записью в лог.
func toolError(err error) error {
	if errFull, ok := errs.AsErrFull(err); ok {
		return fmt.Errorf("%s: %s", errFull.Err, errFull.Desc)
	}

	var semantic errs.Err
	if errors.As(err, &semantic) {
		return err
	}

	slog.Error("tool call failed", "error", err)
	return errors.New("internal error: " + err.Error())
}
