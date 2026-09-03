package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/mechta-market/pulse/internal/errs"
	lokiModel "github.com/mechta-market/pulse/internal/service/loki/model"
)

const queryRangePath = "/loki/api/v1/query_range"

func (s *Service) QueryRange(ctx context.Context, query string, start, end time.Time, limit int) ([]lokiModel.Stream, error) {
	params := url.Values{
		"query":     {query},
		"start":     {strconv.FormatInt(start.UnixNano(), 10)},
		"end":       {strconv.FormatInt(end.UnixNano(), 10)},
		"limit":     {strconv.Itoa(limit)},
		"direction": {"backward"},
	}

	rep := &queryRangeRep{}
	if _, err := s.sendRequest(ctx, http.MethodGet, queryRangePath, params, rep); err != nil {
		return nil, fmt.Errorf("query_range: %w", err)
	}
	if rep.Status != "success" {
		return nil, fmt.Errorf("%w: loki: %s", errs.InvalidRequest, rep.Status)
	}
	if rep.Data.ResultType != "streams" {
		return nil, fmt.Errorf("%w: loki: expected streams, got %s (metric queries are not supported)", errs.InvalidRequest, rep.Data.ResultType)
	}

	var result []streamRep
	if err := json.Unmarshal(rep.Data.Result, &result); err != nil {
		return nil, fmt.Errorf("decode streams: %w", err)
	}

	streams := make([]lokiModel.Stream, 0, len(result))
	for _, item := range result {
		stream := lokiModel.Stream{Labels: item.Stream, Entries: make([]lokiModel.Entry, 0, len(item.Values))}
		for _, v := range item.Values {
			ns, err := strconv.ParseInt(v[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("decode entry timestamp %q: %w", v[0], err)
			}
			stream.Entries = append(stream.Entries, lokiModel.Entry{TS: time.Unix(0, ns).UTC(), Line: v[1]})
		}
		streams = append(streams, stream)
	}

	return streams, nil
}

// transport models

type queryRangeRep struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

type streamRep struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}
