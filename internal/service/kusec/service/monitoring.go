package service

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"

	kusecModel "github.com/mechta-market/pulse/internal/service/kusec/model"
)

func (s *Service) Resolve(ctx context.Context, namespace, kubeName string) (*kusecModel.Resolved, error) {
	key := namespace + "/" + kubeName
	if cached, ok := s.resolved.Load(key); ok {
		if entry := cached.(resolvedEntry); time.Since(entry.at) < resolveTTL {
			return entry.value, nil
		}
	}

	rep := &resolveRep{}
	if err := s.sendRequest(ctx, "/app/resolve", url.Values{"namespace": {namespace}, "kube_name": {kubeName}}, rep); err != nil {
		return nil, err
	}

	result := rep.decode()
	s.resolved.Store(key, resolvedEntry{value: result, at: time.Now()})
	return result, nil
}

func (s *Service) ListAudit(ctx context.Context, req *kusecModel.AuditReq) ([]kusecModel.AuditEntry, error) {
	query := timeWindow(req.Since, req.Until, "created_at")
	query.Set("app_id", req.AppId)

	result := make([]kusecModel.AuditEntry, 0, 16)
	err := s.paginate(ctx, req.Limit, query, func(q url.Values) (int, error) {
		rep := &auditListRep{}
		if err := s.sendRequest(ctx, "/audit", q, rep); err != nil {
			return 0, err
		}
		result = append(result, lo.Map(rep.Results, decodeAuditEntry)...)
		return len(rep.Results), nil
	})
	if err != nil {
		return nil, err
	}
	return lo.Subset(result, 0, uint(max(req.Limit, 0))), nil
}

func (s *Service) ListSyncRuns(ctx context.Context, req *kusecModel.SyncRunReq) ([]kusecModel.SyncRun, error) {
	query := timeWindow(req.Since, req.Until, "started_at")
	query.Set("app_id", req.AppId)

	runs := make([]kusecModel.SyncRun, 0, 8)
	err := s.paginate(ctx, req.Limit, query, func(q url.Values) (int, error) {
		rep := &syncRunListRep{}
		if err := s.sendRequest(ctx, "/sync-run", q, rep); err != nil {
			return 0, err
		}
		runs = append(runs, lo.Map(rep.Results, decodeSyncRun)...)
		return len(rep.Results), nil
	})
	if err != nil {
		return nil, err
	}
	runs = lo.Subset(runs, 0, uint(max(req.Limit, 0)))

	// объекты запуска отдаёт только Get
	var mu sync.Mutex
	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(syncRunConcurrency)
	for i := range runs {
		eg.Go(func() error {
			rep := &syncRunRep{}
			if err := s.sendRequest(egCtx, "/sync-run/"+url.PathEscape(runs[i].Id), nil, rep); err != nil {
				return err
			}
			mu.Lock()
			runs[i] = decodeSyncRun(*rep, 0)
			mu.Unlock()
			return nil
		})
	}
	if err = eg.Wait(); err != nil {
		return nil, fmt.Errorf("sync-run get: %w", err)
	}

	return runs, nil
}

func (s *Service) GetDrift(ctx context.Context, appId string) (*kusecModel.Drift, error) {
	rep := &driftRep{}
	if err := s.sendRequest(ctx, "/app/"+url.PathEscape(appId)+"/drift", nil, rep); err != nil {
		return nil, err
	}
	return &kusecModel.Drift{InCluster: rep.InCluster, Objects: lo.Map(rep.Objects, decodeDriftObject)}, nil
}

// paginate листает страницы (page с 0) до limit записей или последней неполной страницы.
func (s *Service) paginate(ctx context.Context, limit int, query url.Values, fetch func(url.Values) (int, error)) error {
	if limit <= 0 {
		limit = pageSize
	}
	size := min(limit, pageSize)
	query.Set("list_params.page_size", strconv.Itoa(size))

	for page, fetched := 0, 0; fetched < limit; page++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		query.Set("list_params.page", strconv.Itoa(page))
		n, err := fetch(query)
		if err != nil {
			return err
		}
		fetched += n
		if n < size {
			break
		}
	}
	return nil
}

func timeWindow(since, until time.Time, field string) url.Values {
	query := url.Values{}
	if !since.IsZero() {
		query.Set(field+"_gte", since.UTC().Format(time.RFC3339))
	}
	if !until.IsZero() {
		query.Set(field+"_lt", until.UTC().Format(time.RFC3339))
	}
	return query
}
