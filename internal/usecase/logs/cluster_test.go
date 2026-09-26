package logs

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	logsModel "github.com/mechta-market/pulse/internal/domain/logs/model"
	logsService "github.com/mechta-market/pulse/internal/domain/logs/service"
	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
	workloadModel "github.com/mechta-market/pulse/internal/domain/workload/model"
	"github.com/mechta-market/pulse/internal/errs"
	lokiModel "github.com/mechta-market/pulse/internal/service/loki/model"
	lokiService "github.com/mechta-market/pulse/internal/service/loki/service"
	"github.com/mechta-market/pulse/internal/usecase/logs/model"
)

func newClusterUsecase(loki LokiI) *Usecase {
	wl := &fakeWorkload{items: []*workloadModel.Main{
		{Namespace: "prod", Kind: "Deployment", Name: "orders", ServiceName: "orders"},
		{Namespace: "prod", Kind: "Deployment", Name: "orders-worker", ServiceName: "orders"},
		{Namespace: "prod", Kind: "Deployment", Name: "sms", ServiceName: "sms"},
		{Namespace: "prod", Kind: "Deployment", Name: "sms-im", ServiceName: "sms-im"},
	}}
	return New(Config{MaxLines: 5000, MaxPatterns: 20, RawLimit: 100, MaxWindow: 24 * time.Hour},
		&fakeSvc{service: &svcModel.Main{}}, wl, &fakeK8s{}, loki, logsService.New(), testPii)
}

func streamLabels(namespace, pod string) map[string]string {
	return map[string]string{"kubernetes_namespace_name": namespace, "kubernetes_pod_name": pod}
}

func TestQuery_SearchAllServices(t *testing.T) {
	now := time.Now()
	loki := &fakeLoki{streams: []lokiModel.Stream{
		{Labels: streamLabels("prod", "orders-worker-7d9f-q2"), Entries: []lokiModel.Entry{
			{TS: now.Add(-time.Minute), Line: `{"level":"info","msg":"order ORD-12345 paid","phone":"+77011234567"}`},
			{TS: now.Add(-3 * time.Minute), Line: `{"level":"info","msg":"order ORD-12345 created"}`},
		}},
		{Labels: streamLabels("prod", "sms-im-5c6d-x1"), Entries: []lokiModel.Entry{
			{TS: now.Add(-2 * time.Minute), Line: `level=info msg="sms sent" order=ORD-12345`},
		}},
		{Labels: streamLabels("infra", "gateway-1"), Entries: []lokiModel.Entry{
			{TS: now.Add(-4 * time.Minute), Line: `GET /orders/ORD-12345 200`},
		}},
	}}
	u := newClusterUsecase(loki)

	// дефолты поиска: raw, назад по суткам до пустых дней перед следами
	res, err := u.Query(context.Background(), &model.QueryReq{Pattern: "ORD-12345"})
	require.NoError(t, err)
	assert.Equal(t, model.SearchStopFound, res.SearchStop)
	assert.Len(t, loki.calls, 1+emptyDaysAfterHits)
	assert.Equal(t, time.Duration(1+emptyDaysAfterHits)*24*time.Hour, res.End.Sub(res.Start).Round(time.Hour))

	assert.Equal(t, `{kubernetes_namespace_name=~".+"} |= "ORD-12345" |~ "(?:^|[^[:alnum:]])ORD-12345(?:[^[:alnum:]]|$)"`, loki.query, "идентификатор — отдельно стоящим")
	assert.Equal(t, 5000-4, loki.limit, "счётчики по сервисам — по всей выборке max_lines")
	assert.Equal(t, model.ModeRaw, res.Mode)
	assert.Empty(t, res.Service)
	assert.Equal(t, 4, res.TotalLines)
	require.Len(t, res.Services, 3)
	assert.Equal(t, "orders", res.Services[0].Service)
	assert.Equal(t, 2, res.Services[0].Count)
	assert.Equal(t, now.Add(-3*time.Minute), res.Services[0].FirstSeen)

	// строки — по времени, от ранних
	require.Len(t, res.Lines, 4)
	assert.Empty(t, res.Lines[0].Service, "под не из каталога")
	assert.Equal(t, "infra", res.Lines[0].Namespace)
	assert.Equal(t, "orders", res.Lines[1].Service)
	assert.Equal(t, "sms-im", res.Lines[2].Service, "sms-im-…, а не sms")
	assert.Equal(t, "orders-worker", res.Lines[3].Workload, "самый длинный префикс workload'а")
	assert.Contains(t, res.Lines[3].Text, "7011234567", "телефон как есть: от модели прячет агент")

	res, err = u.Query(context.Background(), &model.QueryReq{Pattern: "ORD-12345", Limit: 2})
	require.NoError(t, err)
	require.Len(t, res.Lines, 2, "показаны последние строки")
	assert.Equal(t, "sms-im", res.Lines[0].Service)
	assert.True(t, res.Truncated)
	assert.Equal(t, 4, res.TotalLines)
	require.Len(t, res.Services, 3, "счётчики — по всем найденным строкам")

	res, err = u.Query(context.Background(), &model.QueryReq{Pattern: `ORD-\d+`, Mode: model.ModePatterns})
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(loki.query, `|~ "ORD-\\d+"`), "регэксп — как есть")
	require.Len(t, res.Patterns, 4)
	services := lo.FlatMap(res.Patterns, func(p logsModel.Pattern, _ int) []string { return p.Services })
	assert.ElementsMatch(t, []string{"orders", "orders", "sms-im"}, services)
	assert.True(t, lo.EveryBy(res.Patterns, func(p logsModel.Pattern) bool { return len(p.Workloads) == 0 }))
}

func TestQuery_SearchScanDays(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	at := func(ago time.Duration, text string) lokiModel.Entry {
		return lokiModel.Entry{TS: now.Add(-ago), Line: text}
	}
	loki := &fakeLoki{streams: []lokiModel.Stream{{Labels: streamLabels("prod", "orders-7d9f-q2"), Entries: []lokiModel.Entry{
		// заказ: оформлен 6 дней назад, день тишины, доставлен 4 дня назад
		at(6*day+time.Hour, "order 234115 created"),
		at(4*day+time.Hour, "order 234115 delivered"),
		// тот же номер месяц назад — за пустыми днями, не должен найтись
		at(20*day, "order 234115 archived"),
	}}}}
	u := newClusterUsecase(loki)

	res, err := u.Query(context.Background(), &model.QueryReq{Pattern: "234115"})
	require.NoError(t, err)
	assert.Equal(t, model.SearchStopFound, res.SearchStop)
	require.Len(t, res.Lines, 2, "день тишины между следами не останавливает поиск")
	assert.Contains(t, res.Lines[0].Text, "created")
	assert.Contains(t, res.Lines[1].Text, "delivered")
	assert.Len(t, loki.calls, 9, "сегодня…6 дней назад и два пустых дня перед следами")

	// названный день — только эти сутки, без прохода по дням
	loki.calls = nil
	end := now.Add(-4 * day)
	res, err = u.Query(context.Background(), &model.QueryReq{Pattern: "234115", End: end, EndIsDay: true})
	require.NoError(t, err)
	assert.Empty(t, res.SearchStop)
	assert.Len(t, loki.calls, 1)
	assert.Equal(t, 24*time.Hour, res.End.Sub(res.Start))
	require.Len(t, res.Lines, 1)
	assert.Contains(t, res.Lines[0].Text, "delivered")

	// глубже срока хранения — ошибка с подсказкой
	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: "234115", End: now.Add(-40 * day)})
	require.ErrorIs(t, err, errs.InvalidRequest)
	assert.ErrorContains(t, err, "logs are kept for")
}

func TestQuery_SearchScanBudgetAndRetention(t *testing.T) {
	// номера нет: проход до конца хранения логов
	loki := &fakeLoki{}
	u := newClusterUsecase(loki)
	u.conf.Retention = 5 * 24 * time.Hour
	res, err := u.Query(context.Background(), &model.QueryReq{Pattern: "234115"})
	require.NoError(t, err)
	assert.Equal(t, model.SearchStopRetention, res.SearchStop)
	assert.Len(t, loki.calls, 5)

	// Loki медленный: бюджет кончился — отдаём проверенное, start — докуда успели
	loki = &fakeLoki{delay: 30 * time.Millisecond}
	u = newClusterUsecase(loki)
	u.conf.SearchBudget = 100 * time.Millisecond
	res, err = u.Query(context.Background(), &model.QueryReq{Pattern: "234115"})
	require.NoError(t, err)
	assert.Equal(t, model.SearchStopBudget, res.SearchStop)
	days := int(res.End.Sub(res.Start).Round(time.Hour) / (24 * time.Hour))
	assert.Equal(t, len(loki.calls)-1, days, "последние сутки не успели — в окно не входят")

	// не успели даже первые сутки — ошибка
	u.conf.SearchBudget = 10 * time.Millisecond
	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: "234115"})
	require.Error(t, err)
}

func TestSearchQL(t *testing.T) {
	sel := `{ns=~".+"}`
	assert.Equal(t, `{ns=~".+"} |= "234115" |~ "(?:^|[^[:alnum:].])234115(?:[^[:alnum:]]|$)"`, searchQL(sel, "", "234115", ""), "число — не в дробной части")
	assert.Equal(t, searchQL(sel, "", "234115", ""), searchQL(sel, "", `\b234115\b`, ""), "\\b от модели — тот же поиск подстрокой")
	assert.Equal(t, `{ns=~".+"} |= "ORD-1" |~ "(?:^|[^[:alnum:]])ORD-1(?:[^[:alnum:]]|$)"`, searchQL(sel, "", "ORD-1", ""))
	assert.Equal(t, `{ns=~".+"} |~ "ORD-\\d+"`, searchQL(sel, "", `ORD-\d+`, ""))

	number := regexp.MustCompile(`(?:^|[^[:alnum:].])234115(?:[^[:alnum:]]|$)`)
	for line, want := range map[string]bool{
		`order 234115 created`: true,
		`{"ord_id":234115}`:    true,
		`/orders/234115`:       true,
		`234115`:               true,
		`"id":"halyk__234115"`: true,
		`ord-234115-1`:         true,
		`I0925 06:02:28.234115       1 reflector`: false,
		`order 12341156`: false,
		`order 1234115`:  false,
		`order x234115`:  false,
	} {
		assert.Equal(t, want, number.MatchString(line), line)
	}

	code := regexp.MustCompile(`(?:^|[^[:alnum:]])ORD-1(?:[^[:alnum:]]|$)`)
	assert.True(t, code.MatchString(`"ord":"ORD-1"`))
	assert.True(t, code.MatchString(`x_ORD-1_y`))
	assert.False(t, code.MatchString(`ORD-12`))
	assert.False(t, code.MatchString(`XORD-1`))
}

func TestQuery_SearchValidation(t *testing.T) {
	u := newClusterUsecase(&fakeLoki{})

	_, err := u.Query(context.Background(), &model.QueryReq{})
	require.ErrorIs(t, err, errs.InvalidRequest)
	assert.ErrorContains(t, err, "service is required")

	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: "12"})
	require.ErrorIs(t, err, errs.InvalidRequest)

	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: "12345", Workload: "orders"})
	require.ErrorIs(t, err, errs.InvalidRequest)

	_, err = newClusterUsecase(nil).Query(context.Background(), &model.QueryReq{Pattern: "12345"})
	require.ErrorIs(t, err, errs.ServiceNA)
	assert.ErrorContains(t, err, "requires Loki")
}

func TestClusterErrors(t *testing.T) {
	now := time.Now()
	loki := &fakeLoki{
		vector: []lokiModel.Sample{
			{Labels: streamLabels("prod", "orders-7d9f-q2"), Value: 30},
			{Labels: streamLabels("prod", "orders-worker-5c6d-x1"), Value: 12},
			{Labels: streamLabels("prod", "sms-8f7e-a1"), Value: 5},
			{Labels: streamLabels("infra", "gateway-1"), Value: 3},
			{Labels: streamLabels("prod", "sms-8f7e-a2"), Value: 0},
		},
		streams: []lokiModel.Stream{
			{Labels: streamLabels("prod", "orders-7d9f-q2"), Entries: []lokiModel.Entry{
				{TS: now.Add(-time.Minute), Line: `{"level":"error","msg":"get order 101","error":"context deadline exceeded"}`},
				{TS: now.Add(-2 * time.Minute), Line: `{"level":"error","msg":"get order 102","error":"context deadline exceeded"}`},
			}},
		},
	}
	u := newClusterUsecase(loki)

	res, err := u.ClusterErrors(context.Background(), 48*time.Hour, 2)
	require.NoError(t, err)

	assert.Equal(t, 24*time.Hour, res.Window, "окно ограничено потолком логов")
	assert.Contains(t, loki.vectorQuery, "count_over_time(")
	assert.Contains(t, loki.vectorQuery, "[86400s]")
	assert.Contains(t, loki.vectorQuery, "sum by (namespace, kubernetes_namespace_name")

	assert.Equal(t, 50, res.Total)
	assert.Equal(t, 3, res.ServicesTotal)
	require.Len(t, res.Services, 2)
	assert.Equal(t, "orders", res.Services[0].Service)
	assert.Equal(t, 42, res.Services[0].Count, "workload'ы одного сервиса суммируются")
	assert.Equal(t, 2, res.Services[0].Top.Count)
	assert.Equal(t, "get order <NUM>: context deadline exceeded", res.Services[0].Top.Template)
	assert.Equal(t, "sms", res.Services[1].Service)
	assert.Zero(t, res.Services[1].Top.Count, "строк сервиса нет в выборке — без паттерна")

	_, err = newClusterUsecase(nil).ClusterErrors(context.Background(), time.Hour, 10)
	require.ErrorIs(t, err, errs.ServiceNA)
}

func TestErrorLineRe(t *testing.T) {
	re := regexp.MustCompile(errorLineRe)
	for line, want := range map[string]bool{
		`{"level":"error","msg":"boom"}`:                            true,
		`level=ERROR msg="boom"`:                                    true,
		`{"log":"{\"level\":\"error\",\"msg\":\"boom\"}\n"}`:        true,
		`2026-09-25 ERROR payment failed`:                           true,
		`panic: runtime error: index out of range`:                  true,
		`{"severity":"CRITICAL"}`:                                   true,
		`{"level":"info","msg":"no error, errors=0"}`:               false,
		`level=info msg="retry after error"`:                        false,
		`level=info query="{app=\"x\"} | detected_level=\"error\""`: false,
		`{"log":"{\"level\":\"info\",\"msg\":\"error=nil\"}"}`:      false,
	} {
		assert.Equal(t, want, re.MatchString(line), line)
	}
}

// Живой прогон запросов против настоящего Loki (синтаксис LogQL, sum by по отсутствующим
// лейблам): LOKI_LIVE_URL=http://localhost:3100 go test ./internal/usecase/logs/ -run TestLive -v
func TestLive_ClusterQueries(t *testing.T) {
	url := os.Getenv("LOKI_LIVE_URL")
	if url == "" {
		t.Skip("LOKI_LIVE_URL is not set")
	}
	u := newClusterUsecase(lokiService.New(url, lokiService.Auth{}))

	errors, err := u.ClusterErrors(context.Background(), time.Hour, 10)
	require.NoError(t, err)
	t.Logf("cluster errors: total=%d services=%d", errors.Total, errors.ServicesTotal)
	for _, s := range errors.Services {
		t.Logf("  %s/%s: %d, top=%q", s.Namespace, s.Service, s.Count, s.Top.Template)
	}

	search, err := u.Query(context.Background(), &model.QueryReq{Pattern: lo.CoalesceOrEmpty(os.Getenv("LOKI_LIVE_PATTERN"), "ORD-12345")})
	require.NoError(t, err)
	t.Logf("search: stop=%s start=%s total=%d", search.SearchStop, search.Start.Format(time.RFC3339), search.TotalLines)
	for _, h := range search.Services {
		t.Logf("  hits %s/%s: %d", h.Namespace, h.Service, h.Count)
	}
	for _, l := range search.Lines {
		t.Logf("  line %s %s: %s", l.Service, l.Workload, l.Text)
	}

	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: `ORD-\d+`, Level: "info"})
	require.NoError(t, err)
}

// Телефон в строке логов — как есть (от модели прячет агент), карта — маской; поиск номера
// (+…) по всем сервисам — регэкспом номера в любом написании.
func TestQuery_SearchByPhone(t *testing.T) {
	now := time.Now()
	loki := &fakeLoki{streams: []lokiModel.Stream{
		{Labels: streamLabels("prod", "orders-worker-7d9f-q2"), Entries: []lokiModel.Entry{
			{TS: now.Add(-time.Minute), Line: `{"level":"info","msg":"order ORD-1 paid","phone":"+7 701 123 45 67","card":"4111111111111111"}`},
		}},
	}}
	u := newClusterUsecase(loki)

	res, err := u.Query(context.Background(), &model.QueryReq{Pattern: "ORD-1", Window: time.Hour})
	require.NoError(t, err)
	require.Len(t, res.Lines, 1)
	assert.Contains(t, res.Lines[0].Text, "+7 701 123 45 67")
	assert.Contains(t, res.Lines[0].Text, `"card":"***1111"`)

	_, err = u.Query(context.Background(), &model.QueryReq{Pattern: "+77011234567", Window: time.Hour})
	require.NoError(t, err)
	assert.Contains(t, loki.query, `|= "7011234567" |~ "(?:^|[^0-9])(?:\\+?7|8)?7011234567(?:[^0-9]|$)"`,
		"в Loki — подстрока-предфильтр и точный регэксп номера")
}

// Номер, похожий на объект сервиса по формату из манифеста (domain), — подсказка id_matches.
func TestQuery_IdMatches(t *testing.T) {
	loki := &fakeLoki{}
	u := newClusterUsecase(loki)
	u.svc = &fakeSvc{service: &svcModel.Main{Name: "caravan", Metadata: svcModel.Metadata{Domain: &svcModel.Domain{
		Entities: []svcModel.Entity{{Name: "доставка", IdPattern: "[0-9]{7}"}, {Name: "рейс", IdPattern: "R-[0-9]+"}},
	}}}}

	res, err := u.Query(context.Background(), &model.QueryReq{Pattern: "7784512", Window: time.Hour})
	require.NoError(t, err)
	assert.Equal(t, []model.IdMatch{{Service: "caravan", Entity: "доставка"}}, res.IdMatches)

	res, err = u.Query(context.Background(), &model.QueryReq{Pattern: "timeout.*1c", Window: time.Hour})
	require.NoError(t, err)
	assert.Empty(t, res.IdMatches, "регэксп — не номер")
}
