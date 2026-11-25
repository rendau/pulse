// Package edge — журнал входящих запросов edge-прокси (traefik) в Loki: сводка внешнего
// трафика по адресам клиентов (кто сколько, признаки взлома, чувствительные ручки, новые
// адреса — по просьбе) и хронология одного адреса. Для агента, который по расписанию ищет атаки.
//
// Нагрузка на Loki: журнал — сотни мегабайт в час, а у Loki нет индекса по словам, и любой
// запрос читает все строки окна. Поэтому считают метрические запросы (count_over_time) с
// грубым фильтром подстрокой до | json, строго по одному (общий замок usecase), с таймаутом
// на запрос, общим дедлайном и бюджетом запросов; сводка кэшируется на несколько минут.
// Loki не отдаёт больше max_query_series серий даже в промежуточной группировке: если адресов
// больше, запрос разбивается на части по окончанию адреса (shard).
package edge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/samber/lo"

	edgeModel "github.com/rendau/pulse/internal/domain/edge/model"
	"github.com/rendau/pulse/internal/errs"
	lokiModel "github.com/rendau/pulse/internal/service/loki/model"
	"github.com/rendau/pulse/internal/usecase/edge/model"
)

// Config — журнал и лимиты (yaml-правила edge и env со своими адресами).
type Config struct {
	// Selector — LogQL-селектор журнала входящих запросов; пусто — инструменты выключены
	Selector string
	// InternalRouterPrefix — начало имени роутера внутренней двери (запросы не извне)
	InternalRouterPrefix string
	// Sensitive — части путей чувствительных ручек, по порядку: путь относится к первой подошедшей
	Sensitive []string
	Known     []edgeModel.Known

	Top          int
	MaxWindow    time.Duration
	CacheTTL     time.Duration
	QueryTimeout time.Duration
	Deadline     time.Duration
	MaxQueries   int
	// NewLookback — сколько до окна адрес должен отсутствовать, чтобы считаться новым; NewBudget —
	// время на эту проверку (журнал читается по суткам)
	NewLookback time.Duration
	NewBudget   time.Duration

	ActivityMaxLines  int
	ActivityMaxGroups int
	ActivityPageLines int
}

type Usecase struct {
	conf Config
	loki LokiI

	// mu — запросы к Loki строго по одному: Loki одним инстансом падал от параллельных тяжёлых
	// запросов. Под ним же — кэши.
	mu     sync.Mutex
	cache  map[string]cacheEntry
	seen   map[string]seenEntry
	absent map[string]absentEntry
}

type cacheEntry struct {
	at     time.Time
	result *model.Traffic
}

func New(conf Config, loki LokiI) *Usecase {
	if conf.Top <= 0 {
		conf.Top = 25
	}
	if conf.MaxWindow <= 0 {
		conf.MaxWindow = 24 * time.Hour
	}
	if conf.CacheTTL <= 0 {
		conf.CacheTTL = 5 * time.Minute
	}
	if conf.QueryTimeout <= 0 {
		conf.QueryTimeout = time.Minute
	}
	if conf.Deadline <= 0 {
		conf.Deadline = 150 * time.Second
	}
	if conf.MaxQueries <= 0 {
		conf.MaxQueries = 60
	}
	if conf.NewLookback <= 0 {
		conf.NewLookback = 7 * 24 * time.Hour
	}
	if conf.NewBudget <= 0 {
		conf.NewBudget = 45 * time.Second
	}
	if conf.ActivityMaxLines <= 0 {
		conf.ActivityMaxLines = 50000
	}
	if conf.ActivityMaxGroups <= 0 {
		conf.ActivityMaxGroups = 200
	}
	if conf.ActivityPageLines <= 0 {
		// строка журнала ≈1.5 КБ: 2000 строк — ≈3–4 МБ ответа Loki (предел тела 8 МБ)
		conf.ActivityPageLines = 2000
	}
	return &Usecase{conf: conf, loki: loki, cache: map[string]cacheEntry{}, seen: map[string]seenEntry{},
		absent: map[string]absentEntry{}}
}

func (u *Usecase) check() error {
	if u.loki == nil {
		return fmt.Errorf("%w: requires Loki (LOKI_URL is not configured)", errs.ServiceNA)
	}
	if u.conf.Selector == "" {
		return fmt.Errorf("%w: edge access log is not configured (rules: edge.selector)", errs.ServiceNA)
	}
	return nil
}

// errNoSharding — адресов больше лимита Loki, а окно слишком длинное, чтобы считать частями
var errNoSharding = errors.New("too many series for a long window")

// errBudget — время или число запросов вызова кончились: ответ — то, что успели посчитать
var errBudget = errors.New("budget exhausted")

// run — один вызов инструмента: запросы к Loki по одному, учёт бюджета и ошибок источника.
type run struct {
	u        *Usecase
	ctx      context.Context
	at       time.Time // конец окна
	queries  int
	maxQuery int
	errors   []model.SourceError
	partial  bool
}

func (u *Usecase) newRun(ctx context.Context, at time.Time) *run {
	return &run{u: u, ctx: ctx, at: at, maxQuery: u.conf.MaxQueries}
}

// vector — один метрический запрос на момент at с таймаутом на запрос.
func (r *run) vector(query string, at time.Time) ([]lokiModel.Sample, error) {
	if r.queries >= r.maxQuery || r.ctx.Err() != nil {
		return nil, errBudget
	}
	r.queries++
	qctx, cancel := context.WithTimeout(r.ctx, r.u.conf.QueryTimeout)
	defer cancel()
	t0 := time.Now()
	samples, err := r.u.loki.QueryVector(qctx, query, at)
	slog.Debug("edge: loki query", "took", time.Since(t0).Round(time.Millisecond).String(), "series", len(samples),
		"error", err, "query", query)
	if err != nil && r.ctx.Err() != nil {
		return nil, errBudget
	}
	return samples, err
}

// minStepTime — меньше времени до дедлайна вызова — шаг не начинается: час журнала — ~3 с на
// запрос, но под нагрузкой Loki отвечает и по 20–30 с
const minStepTime = 10 * time.Second

// left — сколько осталось до дедлайна вызова.
func (r *run) left() time.Duration {
	if deadline, ok := r.ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return r.u.conf.Deadline
}

// enough — хватает ли времени на шаг; нет — шаг пропущен с причиной в errors. Шаги идут по
// важности: адреса, признаки взлома, чувствительные ручки, подробности, новые адреса.
func (r *run) enough(step string) bool {
	if r.left() >= minStepTime && r.queries < r.maxQuery {
		return true
	}
	r.fail(step, errBudget)
	return false
}

// fail — шаг не посчитан (или посчитан частично): причина — в errors ответа.
func (r *run) fail(step string, err error) {
	r.partial = true
	msg := err.Error()
	switch {
	case errors.Is(err, errNoSharding):
		msg = fmt.Sprintf("разных адресов больше, чем Loki отдаёт за запрос (limits_config.max_query_series, по умолчанию 500), "+
			"а окно длиннее %s частями не считается: смотри по часам (window=1h и end) или подними лимит в Loki", maxShardWindow)
	case errors.Is(err, errBudget):
		msg = "не успели посчитать: кончилось время или число запросов к Loki на вызов"
	case errors.Is(err, lokiModel.ErrTooManySeries):
		msg = "больше разных значений, чем Loki отдаёт за запрос (limits_config.max_query_series, по умолчанию 500)"
	}
	r.errors = append(r.errors, model.SourceError{Source: "loki", Message: step + ": " + msg})
}

// sharded — запрос с группировкой по ip; не влез в лимит серий Loki — по частям адресов
// (окончанию адреса), а не влезшая часть — мельче. complete=false — часть адресов не посчитана
// (причина — err).
//
// Частями — только окна до maxShardWindow: адресов за сутки — десятки тысяч, это сотни запросов.
func (r *run) sharded(win time.Duration, build func(ipFilter string) string) (samples []lokiModel.Sample, complete bool, err error) {
	samples, err = r.vector(build(""), r.at)
	if !errors.Is(err, lokiModel.ErrTooManySeries) {
		return samples, err == nil, err
	}
	if win > maxShardWindow {
		return nil, false, fmt.Errorf("%w: %w", errNoSharding, err)
	}

	samples = nil
	queue := initialShards()
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		part, err := r.vector(build(s.filter()), r.at)
		switch {
		case err == nil:
			samples = append(samples, part...)
		case errors.Is(err, lokiModel.ErrTooManySeries) && !s.other && len(s.suffix) < maxShardDepth:
			queue = append(queue, s.split()...)
		default:
			return samples, false, err
		}
	}
	return samples, true, nil
}

func errorsIsSeries(err error) bool { return errors.Is(err, lokiModel.ErrTooManySeries) }

// maxShardWindow — окна длиннее частями не считаются (см. sharded)
const maxShardWindow = 2 * time.Hour

// within — шаг с собственным потолком времени внутри дедлайна вызова: тяжёлый шаг не съедает
// время следующих.
func (r *run) within(d time.Duration, step func()) {
	ctx, cancel := context.WithTimeout(r.ctx, d)
	defer cancel()
	parent := r.ctx
	r.ctx = ctx
	defer func() { r.ctx = parent }()
	step()
}

// maxShardDepth — глубже части не делятся: третий символ с конца — это уже десятки запросов
const maxShardDepth = 3

// chunked — запрос по списку адресов (лейблов ip); не влез в лимит серий — по половинам.
// Адрес, у которого одного значений больше лимита, — в over.
func (r *run) chunked(labels []string, build func(labels []string) string) (samples []lokiModel.Sample, over []string, err error) {
	return r.chunkedBy(labels, max(len(labels), 1), build)
}

// chunkedBy — chunked, начиная с частей по size адресов.
func (r *run) chunkedBy(labels []string, size int, build func(labels []string) string) (samples []lokiModel.Sample, over []string, err error) {
	queue := lo.Chunk(labels, size)
	for len(queue) > 0 {
		chunk := queue[0]
		queue = queue[1:]
		if len(chunk) == 0 {
			continue
		}
		part, err := r.vector(build(chunk), r.at)
		switch {
		case err == nil:
			samples = append(samples, part...)
		case errors.Is(err, lokiModel.ErrTooManySeries) && len(chunk) > 1:
			queue = append(queue, chunk[:len(chunk)/2], chunk[len(chunk)/2:])
		case errors.Is(err, lokiModel.ErrTooManySeries):
			over = append(over, chunk[0])
		default:
			return samples, over, err
		}
	}
	return samples, over, nil
}

// period — окно запроса: end (нулевой — сейчас) и window с потолком.
func (u *Usecase) period(win time.Duration, end time.Time) (time.Time, time.Time, error) {
	if win <= 0 {
		win = time.Hour
	}
	if win > u.conf.MaxWindow {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: window exceeds maximum %s", errs.InvalidRequest, u.conf.MaxWindow)
	}
	now := time.Now()
	if end.IsZero() || end.After(now) {
		end = now
	}
	return end.Add(-win), end, nil
}
