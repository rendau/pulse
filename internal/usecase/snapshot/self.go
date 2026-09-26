package snapshot

import (
	"context"
	"errors"
)

// selfReport — что сервис сообщает о себе сам (ручка состояния по манифесту): service/selfreport.
func (c *collector) selfReport(ctx context.Context) {
	if c.u.self == nil {
		return
	}
	report, failures := c.u.self.Report(ctx, c.service, c.workloads)
	for _, f := range failures {
		c.addError(f.Source, errors.New(f.Message))
	}
	if report == nil {
		return
	}
	c.mu.Lock()
	c.snap.Self = report
	c.mu.Unlock()
}
