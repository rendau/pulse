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
	prometheusModel "github.com/mechta-market/pulse/internal/service/prometheus/model"
)

const (
	queryPath      = "/api/v1/query"
	queryRangePath = "/api/v1/query_range"
)

func (s *Service) Query(ctx context.Context, promql string, at time.Time) ([]prometheusModel.Sample, error) {
	query := url.Values{"query": {promql}}
	if !at.IsZero() {
		query.Set("time", formatTime(at))
	}

	rep := &queryRep{}
	if _, err := s.sendRequest(ctx, http.MethodGet, queryPath, query, rep); err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	if err := rep.check(); err != nil {
		return nil, err
	}

	var result []vectorItemRep
	switch rep.Data.ResultType {
	case "vector":
		if err := json.Unmarshal(rep.Data.Result, &result); err != nil {
			return nil, fmt.Errorf("decode vector: %w", err)
		}
	case "scalar":
		var scalar [2]any
		if err := json.Unmarshal(rep.Data.Result, &scalar); err != nil {
			return nil, fmt.Errorf("decode scalar: %w", err)
		}
		result = []vectorItemRep{{Value: scalar}}
	}

	samples := make([]prometheusModel.Sample, 0, len(result))
	for _, item := range result {
		point, err := decodePoint(item.Value)
		if err != nil {
			return nil, err
		}
		samples = append(samples, prometheusModel.Sample{Labels: item.Metric, TS: point.TS, Value: point.Value})
	}

	return samples, nil
}

func (s *Service) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) ([]prometheusModel.Series, error) {
	query := url.Values{
		"query": {promql},
		"start": {formatTime(start)},
		"end":   {formatTime(end)},
		"step":  {strconv.FormatInt(int64(step.Seconds()), 10)},
	}

	rep := &queryRep{}
	if _, err := s.sendRequest(ctx, http.MethodGet, queryRangePath, query, rep); err != nil {
		return nil, fmt.Errorf("query_range: %w", err)
	}
	if err := rep.check(); err != nil {
		return nil, err
	}

	var result []matrixItemRep
	if rep.Data.ResultType == "matrix" {
		if err := json.Unmarshal(rep.Data.Result, &result); err != nil {
			return nil, fmt.Errorf("decode matrix: %w", err)
		}
	}

	series := make([]prometheusModel.Series, 0, len(result))
	for _, item := range result {
		points := make([]prometheusModel.Point, 0, len(item.Values))
		for _, v := range item.Values {
			point, err := decodePoint(v)
			if err != nil {
				return nil, err
			}
			points = append(points, point)
		}
		series = append(series, prometheusModel.Series{Labels: item.Metric, Points: points})
	}

	return series, nil
}

// transport models

type queryRep struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

func (r *queryRep) check() error {
	if r.Status != "success" {
		return fmt.Errorf("%w: prometheus %s: %s", errs.InvalidRequest, r.ErrorType, r.Error)
	}
	return nil
}

type vectorItemRep struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}

type matrixItemRep struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"`
}

// decodePoint разбирает пару [unix_ts, "value"].
func decodePoint(v [2]any) (prometheusModel.Point, error) {
	ts, ok := v[0].(float64)
	if !ok {
		return prometheusModel.Point{}, fmt.Errorf("decode point: timestamp %v", v[0])
	}
	raw, ok := v[1].(string)
	if !ok {
		return prometheusModel.Point{}, fmt.Errorf("decode point: value %v", v[1])
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return prometheusModel.Point{}, fmt.Errorf("decode point: %w", err)
	}

	sec, frac := int64(ts), ts-float64(int64(ts))
	return prometheusModel.Point{TS: time.Unix(sec, int64(frac*1e9)).UTC(), Value: value}, nil
}

func formatTime(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixMilli())/1000, 'f', 3, 64)
}
