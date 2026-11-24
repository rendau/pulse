package edge

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/samber/lo"

	edgeModel "github.com/rendau/pulse/internal/domain/edge/model"
	edgeService "github.com/rendau/pulse/internal/domain/edge/service"
	"github.com/rendau/pulse/internal/errs"
	lokiModel "github.com/rendau/pulse/internal/service/loki/model"
	"github.com/rendau/pulse/internal/usecase/edge/model"
)

const (
	maxTop = 50
	// maxSensitiveTop — адресов на чувствительную ручку
	maxSensitiveTop = 10
	maxDetailValues = 3
)

// hostRe — домен фильтра: подставляется в LogQL, поэтому только символы имени хоста
var hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)

func (u *Usecase) Traffic(ctx context.Context, req *model.TrafficReq) (*model.Traffic, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	host := strings.ToLower(strings.TrimSpace(req.Host))
	if host != "" && !hostRe.MatchString(host) {
		return nil, fmt.Errorf("%w: host %q: expected domain name like api.example.com", errs.InvalidRequest, req.Host)
	}
	top := req.Top
	switch {
	case top <= 0:
		top = u.conf.Top
	case top > maxTop:
		top = maxTop
	}
	start, end, err := u.period(req.Window, req.End)
	if err != nil {
		return nil, err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	endKey := "now"
	if !req.End.IsZero() {
		endKey = strconv.FormatInt(end.Unix(), 10)
	}
	key := fmt.Sprintf("%s|%s|%s|%d", host, end.Sub(start), endKey, top)
	if hit, ok := u.cache[key]; ok && time.Since(hit.at) < u.conf.CacheTTL {
		result := *hit.result
		result.CachedAt = hit.at
		return &result, nil
	}

	ctx, cancel := context.WithTimeout(ctx, u.conf.Deadline)
	defer cancel()
	t0 := time.Now()
	r := u.newRun(ctx, end)
	result := r.traffic(host, start, end, top)
	result.Took = time.Since(t0)

	for k, v := range u.cache {
		if time.Since(v.at) >= u.conf.CacheTTL {
			delete(u.cache, k)
		}
	}
	u.cache[key] = cacheEntry{at: time.Now(), result: result}
	return result, nil
}

// clientAgg — счётчики адреса клиента (ключ — edgeService.ClientKey) и лейблы ip, под
// которыми он пришёл из Loki (по ним — запросы только по этому адресу).
type clientAgg struct {
	key      string
	requests float64
	labels   []string
}

type clientSet map[string]*clientAgg

func (s clientSet) add(label string, v float64) *clientAgg {
	key, ok := edgeService.ClientKey(label)
	if !ok {
		return nil
	}
	c, ok := s[key]
	if !ok {
		c = &clientAgg{key: key}
		s[key] = c
	}
	c.requests += v
	if !lo.Contains(c.labels, label) {
		c.labels = append(c.labels, label)
	}
	return c
}

func (r *run) traffic(host string, start, end time.Time, top int) *model.Traffic {
	result := &model.Traffic{Start: start, End: end, Host: host}
	rng := rangeOf(end.Sub(start))
	base := r.u.base(host, false)
	known := r.u.conf.Known

	// 1. запросов по адресам — все адреса окна (для своих и для топа)
	// не больше половины времени вызова: без него признаки взлома и ручки важнее топа
	clients := clientSet{}
	var samples []lokiModel.Sample
	var complete bool
	var err error
	r.within(r.left()/2, func() {
		samples, complete, err = r.sharded(end.Sub(start), func(f string) string {
			return "sum by (ip) (count_over_time(" + base + " " + jsonFields(clientFields(nil)) + " | " + ipLabel + f + " " + rng + "))"
		})
	})
	if err != nil {
		r.fail("запросов по адресам", err)
	}
	for _, s := range samples {
		clients.add(s.Labels["ip"], s.Value)
	}
	if complete {
		result.ClientsTotal = len(clients)
	}

	byVolume := lo.Values(clients)
	sort.Slice(byVolume, func(i, j int) bool {
		if byVolume[i].requests != byVolume[j].requests {
			return byVolume[i].requests > byVolume[j].requests
		}
		return byVolume[i].key < byVolume[j].key
	})
	var strangers, own []*clientAgg
	for _, c := range byVolume {
		if _, ok := edgeService.KnownLabel(known, c.key); ok {
			own = append(own, c)
		} else if len(strangers) < top {
			strangers = append(strangers, c)
		}
	}

	// 2. признаки взлома в путях — по всем адресам
	sig := r.signatures(base, rng, end.Sub(start), clients)
	// 3. чувствительные ручки — по всем адресам (не влезло — по адресам из топа)
	candidates := lo.Uniq(lo.Flatten([][]string{lo.FlatMap(strangers, labelsOf), lo.FlatMap(own, labelsOf), sigLabels(sig, clients)}))
	sensitive, sensHits := r.sensitive(base, rng, candidates, clients)

	// 4. всего запросов по классам кода
	result.Statuses = r.statuses(base, rng)
	result.Requests = lo.SumBy(result.Statuses, func(c model.Count) float64 { return c.Count })
	if result.Requests == 0 && complete {
		result.Requests = lo.SumBy(byVolume, func(c *clientAgg) float64 { return c.requests })
	}

	// топ по объёму не посчитан (окно длинное, а адресов больше лимита Loki) — подробности
	// для чужих адресов с признаками взлома и на чувствительных ручках, самые активные первыми
	if !complete && len(strangers) == 0 {
		strangers = suspects(clients, sig, sensHits, known, top)
	}

	// 5–7. подробности, новые адреса и разных путей — для чужих адресов топа
	// новые адреса важнее числа путей: проверка — до половины оставшегося времени
	details := r.details(base, rng, strangers)
	var fresh map[string]newResult
	r.within(r.left()/2, func() { fresh = r.checkNew(strangers, start) })
	paths, pathsOver := r.distinctPaths(base, rng, strangers, end.Sub(start))

	encode := func(c *clientAgg) model.Client {
		label, _ := edgeService.KnownLabel(known, c.key)
		cl := model.Client{Addr: c.key, Own: label, Requests: c.requests, SensitiveHits: sensHits[c.key]}
		if d, ok := details[c.key]; ok && cl.Requests == 0 {
			cl.Requests = d.total
		}
		if s, ok := sig[c.key]; ok {
			cl.SignatureHits = s.Hits
		}
		if d, ok := details[c.key]; ok {
			cl.Share4xx, cl.Share5xx = d.shares()
			cl.Hosts, cl.UserAgents, cl.Countries = d.hosts.top(maxDetailValues), d.agents.top(maxDetailValues), d.countries.top(maxDetailValues)
		}
		if n, ok := paths[c.key]; ok {
			cl.DistinctPaths = n
		}
		cl.DistinctPathsMore = pathsOver[c.key]
		if f, ok := fresh[c.key]; ok {
			cl.New, cl.NewChecked = new(f.isNew), f.checked
		}
		return cl
	}
	result.Clients = lo.Map(strangers, func(c *clientAgg, _ int) model.Client { return encode(c) })
	result.Own = lo.Map(own, func(c *clientAgg, _ int) model.Client { return encode(c) })

	sigClients := lo.Values(sig)
	sort.Slice(sigClients, func(i, j int) bool {
		if sigClients[i].Hits != sigClients[j].Hits {
			return sigClients[i].Hits > sigClients[j].Hits
		}
		return sigClients[i].Addr < sigClients[j].Addr
	})
	result.Signatures = lo.Map(sigClients[:min(len(sigClients), top)], func(s *model.SignatureClient, _ int) model.SignatureClient {
		out := *s
		out.Own, _ = edgeService.KnownLabel(known, s.Addr)
		if c, ok := clients[s.Addr]; ok {
			out.Requests = c.requests
		}
		sort.Slice(out.Signatures, func(i, j int) bool { return out.Signatures[i].Count > out.Signatures[j].Count })
		return out
	})
	result.Sensitive = sensitive

	result.Partial, result.Errors, result.Queries = r.partial, r.errors, r.queries
	return result
}

func labelsOf(c *clientAgg, _ int) []string { return c.labels }

// suspects — чужие адреса с признаками взлома и обращениями к чувствительным ручкам, больше
// всего — первыми; лейблы ip — из ответов Loki (clients).
func suspects(clients clientSet, sig map[string]*model.SignatureClient, sensHits map[string]float64, known []edgeModel.Known, top int) []*clientAgg {
	score := map[string]float64{}
	for key, s := range sig {
		score[key] += s.Hits
	}
	for key, v := range sensHits {
		score[key] += v
	}
	keys := lo.Filter(lo.Keys(score), func(key string, _ int) bool {
		_, own := edgeService.KnownLabel(known, key)
		return !own && clients[key] != nil
	})
	sort.Slice(keys, func(i, j int) bool {
		if score[keys[i]] != score[keys[j]] {
			return score[keys[i]] > score[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return lo.Map(keys[:min(len(keys), top)], func(key string, _ int) *clientAgg { return clients[key] })
}

// sigLabels — лейблы ip адресов с признаками взлома (адрес без трафика в сводке — как есть).
func sigLabels(sig map[string]*model.SignatureClient, clients clientSet) []string {
	return lo.FlatMap(lo.Keys(sig), func(key string, _ int) []string {
		if c, ok := clients[key]; ok {
			return c.labels
		}
		return []string{key}
	})
}

// statuses — всего запросов по классам кода: фон для долей 4xx/5xx адресов.
func (r *run) statuses(base, rng string) []model.Count {
	if !r.enough("запросов по кодам") {
		return nil
	}
	samples, err := r.vector("sum by (sc) (count_over_time("+base+" "+jsonFields(map[string]string{"status": edgeService.FieldStatus})+
		" | label_format "+statusClassLabel+" "+rng+"))", r.at)
	if err != nil {
		r.fail("запросов по кодам", err)
		return nil
	}
	return statusCounts(lo.Map(samples, func(s lokiModel.Sample, _ int) model.Count {
		return model.Count{Value: s.Labels["sc"], Count: s.Value}
	}))
}

// statusCounts — классы кода 2xx…5xx из первой цифры, по порядку.
func statusCounts(raw []model.Count) []model.Count {
	byClass := map[string]float64{}
	for _, c := range raw {
		class := "other"
		if len(c.Value) == 1 && c.Value[0] >= '1' && c.Value[0] <= '5' {
			class = c.Value + "xx"
		}
		byClass[class] += c.Count
	}
	out := lo.MapToSlice(byClass, func(k string, v float64) model.Count { return model.Count{Value: k, Count: v} })
	sort.Slice(out, func(i, j int) bool { return out[i].Value < out[j].Value })
	return out
}

// signatures — адреса с признаками взлома в путях: сколько и каких.
// Лейблы ip найденных адресов дописываются в clients (без запросов).
func (r *run) signatures(base, rng string, win time.Duration, clients clientSet) map[string]*model.SignatureClient {
	out := map[string]*model.SignatureClient{}
	if !r.enough("признаки взлома") {
		return out
	}
	query := func(f string) string {
		return "sum by (ip, sig) (count_over_time(" + base + anyOf(edgeService.SignaturesSubstrings()) + " " +
			jsonFields(clientFields(map[string]string{"path": edgeService.FieldPath})) + signatureFilter() +
			" | " + ipLabel + ", " + signatureLabel() + f + " " + rng + "))"
	}
	samples, _, err := r.sharded(win, query)
	if err != nil {
		r.fail("признаки взлома", err)
	}

	for _, s := range samples {
		key, ok := edgeService.ClientKey(s.Labels["ip"])
		if !ok {
			continue
		}
		clients.add(s.Labels["ip"], 0)
		c, ok := out[key]
		if !ok {
			c = &model.SignatureClient{Addr: key}
			out[key] = c
		}
		c.Hits += s.Value
		name := lo.FirstOr(edgeService.MatchSignatures(s.Labels["sig"]), "other")
		if i := lo.IndexOf(lo.Map(c.Signatures, func(x model.Count, _ int) string { return x.Value }), name); i >= 0 {
			c.Signatures[i].Count += s.Value
		} else {
			c.Signatures = append(c.Signatures, model.Count{Value: name, Count: s.Value})
		}
	}
	return out
}

// sensitive — обращения к чувствительным ручкам по адресам. Не влезло в лимит серий — по
// каждой ручке отдельно; у популярной ручки (личный кабинет) адресов тысячи — тогда только
// общее число и адреса-кандидаты (топ, свои, с признаками взлома).
func (r *run) sensitive(base, rng string, candidates []string, clients clientSet) ([]model.SensitiveEndpoint, map[string]float64) {
	patterns := r.u.conf.Sensitive
	if len(patterns) == 0 || !r.enough("чувствительные ручки") {
		return nil, nil
	}
	pathJson := jsonFields(clientFields(map[string]string{"path": edgeService.FieldPath}))

	byEp := map[string]map[string]float64{}
	more := map[string]bool{}
	totals := map[string]float64{}
	add := func(ep, label string, v float64) {
		key, ok := edgeService.ClientKey(label)
		if !ok {
			return
		}
		clients.add(label, 0)
		if byEp[ep] == nil {
			byEp[ep] = map[string]float64{}
		}
		byEp[ep][key] += v
	}

	samples, err := r.vector("sum by (ip, ep) (count_over_time("+base+anyOf(patterns)+" "+pathJson+
		" | "+ipLabel+", "+sensitiveLabel(patterns)+` | ep!=""`+" "+rng+"))", r.at)
	switch {
	case err == nil:
		for _, s := range samples {
			add(s.Labels["ep"], s.Labels["ip"], s.Value)
		}
	case errorsIsSeries(err):
		for _, p := range patterns {
			pathFilter := " | path=~" + strconv.Quote("^.*"+regexp.QuoteMeta(p)+".*$")
			samples, err := r.vector("sum by (ip) (count_over_time("+base+anyOf([]string{p})+" "+pathJson+pathFilter+
				" | "+ipLabel+" "+rng+"))", r.at)
			if err == nil {
				for _, s := range samples {
					add(p, s.Labels["ip"], s.Value)
				}
				continue
			}
			if !errorsIsSeries(err) {
				r.fail("чувствительная ручка "+p, err)
				continue
			}
			// адресов у ручки больше лимита: общее число и кандидаты
			more[p] = true
			if total, err := r.vector("sum(count_over_time("+base+anyOf([]string{p})+" "+jsonFields(map[string]string{"path": edgeService.FieldPath})+
				pathFilter+" "+rng+"))", r.at); err == nil && len(total) > 0 {
				totals[p] = total[0].Value
			}
			if len(candidates) == 0 {
				continue
			}
			samples, _, err = r.chunked(candidates, func(labels []string) string {
				return "sum by (ip) (count_over_time(" + base + anyOf([]string{p}) + prefilter(clientSubstrings(labels)) + " " + pathJson + pathFilter +
					" | " + ipLabel + ipIn(labels) + " " + rng + "))"
			})
			if err != nil {
				r.fail("чувствительная ручка "+p+" по адресам топа", err)
			}
			for _, s := range samples {
				add(p, s.Labels["ip"], s.Value)
			}
		}
	default:
		r.fail("чувствительные ручки", err)
	}

	hits := map[string]float64{}
	out := lo.FilterMap(patterns, func(p string, _ int) (model.SensitiveEndpoint, bool) {
		perClient := byEp[p]
		if len(perClient) == 0 && totals[p] == 0 {
			return model.SensitiveEndpoint{}, false
		}
		ep := model.SensitiveEndpoint{Endpoint: p, Clients: len(perClient), ClientsMore: more[p]}
		for key, v := range perClient {
			ep.Requests += v
			hits[key] += v
		}
		if more[p] {
			ep.Requests = max(ep.Requests, totals[p])
		}
		top := lo.MapToSlice(perClient, func(k string, v float64) model.SensitiveClient {
			label, _ := edgeService.KnownLabel(r.u.conf.Known, k)
			return model.SensitiveClient{Addr: k, Own: label, Hits: v}
		})
		sort.Slice(top, func(i, j int) bool {
			if top[i].Hits != top[j].Hits {
				return top[i].Hits > top[j].Hits
			}
			return top[i].Addr < top[j].Addr
		})
		ep.Top = top[:min(len(top), maxSensitiveTop)]
		return ep, true
	})
	return out, hits
}

// detailAgg — подробности адреса: коды, домены, User-Agent, страны.
type detailAgg struct {
	total, c4xx, c5xx float64
	hosts             counter
	agents            counter
	countries         counter
}

func (d *detailAgg) shares() (*float64, *float64) {
	if d.total == 0 {
		return nil, nil
	}
	return new(d.c4xx / d.total), new(d.c5xx / d.total)
}

type counter map[string]float64

// top — самые частые значения (пустые пропускаются).
func (c counter) top(n int) []model.Count {
	out := lo.FilterMap(lo.Entries(c), func(e lo.Entry[string, float64], _ int) (model.Count, bool) {
		return model.Count{Value: e.Key, Count: e.Value}, e.Key != ""
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return out[:min(len(out), n)]
}

// details — коды, домены, User-Agent и страна адресов топа одним запросом (по частям, если
// значений больше лимита; адрес, меняющий User-Agent на каждый запрос, — без User-Agent).
func (r *run) details(base, rng string, clients []*clientAgg) map[string]*detailAgg {
	out := map[string]*detailAgg{}
	if len(clients) == 0 || !r.enough("подробности адресов топа") {
		return out
	}
	fields := clientFields(map[string]string{
		"status": edgeService.FieldStatus, "host": edgeService.FieldHost,
		"ua": edgeService.FieldUserAgent, "country": edgeService.FieldCfCountry,
	})
	build := func(by string, uaLabel string) func(labels []string) string {
		return func(labels []string) string {
			return "sum by (" + by + ") (count_over_time(" + base + prefilter(clientSubstrings(labels)) + " " + jsonFields(fields) +
				" | " + ipLabel + ", " + statusClassLabel + uaLabel + ipIn(labels) + " " + rng + "))"
		}
	}
	samples, over, err := r.chunked(lo.FlatMap(clients, labelsOf), build("ip, sc, host, ua, country", ", ua=`{{ trunc 150 .ua }}`"))
	if err != nil {
		r.fail("подробности адресов топа", err)
	}
	if len(over) > 0 {
		more, _, err := r.chunked(over, build("ip, sc, host, country", ""))
		if err != nil {
			r.fail("подробности адресов топа без User-Agent", err)
		}
		samples = append(samples, more...)
	}

	for _, s := range samples {
		key, ok := edgeService.ClientKey(s.Labels["ip"])
		if !ok {
			continue
		}
		d, ok := out[key]
		if !ok {
			d = &detailAgg{hosts: counter{}, agents: counter{}, countries: counter{}}
			out[key] = d
		}
		d.total += s.Value
		switch s.Labels["sc"] {
		case "4":
			d.c4xx += s.Value
		case "5":
			d.c5xx += s.Value
		}
		d.hosts[edgeService.Clean(s.Labels["host"], 100)] += s.Value
		d.agents[edgeService.Clean(s.Labels["ua"], edgeService.MaxUserAgentLen)] += s.Value
		d.countries[edgeService.Clean(s.Labels["country"], 10)] += s.Value
	}
	return out
}

// longWindowPathChunk — адресов на запрос числа путей за окно длиннее maxShardWindow
const longWindowPathChunk = 6

func chunkSize(win time.Duration, n int) int {
	if win > maxShardWindow {
		return longWindowPathChunk
	}
	return max(n, 1)
}

// distinctPaths — разных путей у адресов топа (сегменты с цифрами склеены). Адрес, у которого
// путей больше лимита серий Loki, — в over: сотни разных путей — уже признак перебора.
func (r *run) distinctPaths(base, rng string, clients []*clientAgg, win time.Duration) (map[string]int, map[string]bool) {
	out, over := map[string]int{}, map[string]bool{}
	if len(clients) == 0 || !r.enough("разных путей у адресов топа") {
		return out, over
	}
	fields := clientFields(map[string]string{"path": edgeService.FieldPath})
	// за длинное окно путей у адреса — десятки: сразу небольшими частями, а не через отказы Loki
	labels := lo.FlatMap(clients, labelsOf)
	samples, overLabels, err := r.chunkedBy(labels, chunkSize(win, len(labels)), func(labels []string) string {
		return "count by (ip) (sum by (ip, np) (count_over_time(" + base + prefilter(clientSubstrings(labels)) + " " + jsonFields(fields) +
			" | " + ipLabel + ", " + pathIdLabel + ipIn(labels) + " " + rng + ")))"
	})
	if err != nil {
		r.fail("разных путей у адресов топа", err)
	}
	for _, s := range samples {
		if key, ok := edgeService.ClientKey(s.Labels["ip"]); ok {
			out[key] += int(s.Value)
		}
	}
	for _, l := range overLabels {
		if key, ok := edgeService.ClientKey(l); ok {
			over[key] = true
		}
	}
	return out, over
}
