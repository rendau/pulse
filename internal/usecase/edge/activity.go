package edge

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/samber/lo"

	edgeModel "github.com/rendau/pulse/internal/domain/edge/model"
	edgeService "github.com/rendau/pulse/internal/domain/edge/service"
	"github.com/rendau/pulse/internal/errs"
	"github.com/rendau/pulse/internal/usecase/edge/model"
)

const activityTopValues = 5

func (u *Usecase) ClientActivity(ctx context.Context, req *model.ActivityReq) (*model.Activity, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	key, ok := edgeService.ClientKey(req.Client)
	if !ok {
		return nil, fmt.Errorf("%w: client %q: expected IPv4/IPv6 address or IPv6 subnet like 2a03:32c0:e000:2545::/64", errs.InvalidRequest, req.Client)
	}
	if strings.Contains(key, "/") && (!strings.Contains(key, ":") || !strings.HasSuffix(key, "/64")) {
		return nil, fmt.Errorf("%w: client %q: pass one address or an IPv6 /64 subnet (as in get_edge_traffic)", errs.InvalidRequest, req.Client)
	}
	start, end, err := u.period(req.Window, req.End)
	if err != nil {
		return nil, err
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, u.conf.Deadline)
	defer cancel()

	query := u.conf.Selector + anyOf([]string{activitySubstring(key)})
	var requests []edgeModel.Request
	// cursor — конец следующей страницы (строки читаются от конца окна назад); covered — докуда дочитано
	cursor, covered, linesTruncated := end, end, false
	for {
		qctx, qcancel := context.WithTimeout(ctx, u.conf.QueryTimeout)
		streams, err := u.loki.QueryRange(qctx, query, start, cursor, u.conf.ActivityPageLines)
		qcancel()
		if err != nil {
			if len(requests) == 0 {
				return nil, fmt.Errorf("loki.QueryRange: %w", err)
			}
			// не успели дочитать: хронология — с того места, докуда дошли
			linesTruncated = true
			break
		}
		lines, oldest := 0, cursor
		for _, s := range streams {
			for _, e := range s.Entries {
				lines++
				oldest = lo.Earliest(oldest, e.TS)
				r, ok := edgeService.ParseRequest(e.Line, e.TS, u.conf.InternalRouterPrefix)
				if !ok {
					continue
				}
				if k, ok := edgeService.ClientKey(r.Client); ok && k == key {
					requests = append(requests, r)
				}
			}
		}
		covered = oldest
		if lines < u.conf.ActivityPageLines || !oldest.After(start) || !oldest.Before(cursor) {
			covered = start
			break
		}
		if len(requests) >= u.conf.ActivityMaxLines {
			linesTruncated = true
			break
		}
		// следующая страница — строго раньше самой старой строки (конец окна Loki не включается)
		cursor = oldest
	}

	result := buildActivity(requests, u.conf.Sensitive, u.conf.ActivityMaxGroups)
	result.Client, result.Start, result.End = key, start, end
	result.Own, _ = edgeService.KnownLabel(u.conf.Known, key)
	result.LinesTruncated = linesTruncated
	if linesTruncated {
		result.CoveredFrom = covered
	}
	return result, nil
}

// activitySubstring — подстрока строки журнала с адресом: IPv4 и целый адрес — в кавычках, подсеть
// IPv6 — начало адреса до первого «::» (сжатая запись внутри подсети — тоже подходит).
func activitySubstring(key string) string {
	if base, ok := strings.CutSuffix(key, "/64"); ok {
		base, _, _ = strings.Cut(base, "::")
		return `"` + base + `:`
	}
	return `"` + key + `"`
}

type groupKey struct {
	method, host, path string
	status             int
	internal           bool
}

// buildActivity — запросы адреса в сжатую хронологию: одинаковые (метод, домен, путь с id, код)
// — одной группой со счётчиком, первым и последним временем. Групп больше maxGroups — остаются
// группы с признаками взлома и чувствительными ручками, затем самые частые.
func buildActivity(requests []edgeModel.Request, sensitive []string, maxGroups int) *model.Activity {
	a := &model.Activity{Requests: len(requests)}
	groups := map[groupKey]*model.ActivityGroup{}
	statuses, hosts, agents, referers, countries, sigs, sens := counter{}, counter{}, counter{}, counter{}, counter{}, counter{}, counter{}

	for _, r := range requests {
		if a.FirstSeen.IsZero() || r.TS.Before(a.FirstSeen) {
			a.FirstSeen = r.TS
		}
		if r.TS.After(a.LastSeen) {
			a.LastSeen = r.TS
		}
		if r.Internal {
			a.Internal++
		}
		host := edgeService.Clean(strings.ToLower(r.Host), 100)
		k := groupKey{
			method:   edgeService.Clean(r.Method, 10),
			host:     host,
			path:     edgeService.Clean(edgeService.NormalizePath(r.Path), edgeService.MaxPathLen),
			status:   r.Status,
			internal: r.Internal,
		}
		g, ok := groups[k]
		if !ok {
			g = &model.ActivityGroup{Method: k.method, Host: k.host, Path: k.path, Status: k.status, Internal: k.internal, First: r.TS, Last: r.TS,
				Signatures: edgeService.MatchSignatures(r.Path)}
			g.Sensitive, _ = lo.Find(sensitive, func(p string) bool { return strings.Contains(r.Path, p) })
			groups[k] = g
		}
		g.Count++
		g.First = lo.Earliest(g.First, r.TS)
		g.Last = lo.Latest(g.Last, r.TS)

		statuses[edgeService.StatusClass(r.Status)]++
		hosts[host]++
		agents[edgeService.Clean(r.UserAgent, edgeService.MaxUserAgentLen)]++
		referers[cleanReferer(r.Referer)]++
		countries[edgeService.Clean(r.Country, 10)]++
		for _, s := range g.Signatures {
			sigs[s]++
		}
		if g.Sensitive != "" {
			sens[g.Sensitive]++
		}
	}

	all := lo.MapToSlice(groups, func(_ groupKey, g *model.ActivityGroup) model.ActivityGroup { return *g })
	a.TotalGroups = len(all)
	if len(all) > maxGroups {
		sort.Slice(all, func(i, j int) bool {
			fi, fj := flagged(all[i]), flagged(all[j])
			if fi != fj {
				return fi
			}
			if all[i].Count != all[j].Count {
				return all[i].Count > all[j].Count
			}
			return all[i].First.Before(all[j].First)
		})
		all, a.Truncated = all[:maxGroups], true
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].First.Equal(all[j].First) {
			return all[i].First.Before(all[j].First)
		}
		return all[i].Path < all[j].Path
	})
	a.Groups = all

	a.Statuses = statuses.top(len(statuses))
	sort.Slice(a.Statuses, func(i, j int) bool { return a.Statuses[i].Value < a.Statuses[j].Value })
	a.Hosts, a.UserAgents, a.Referers = hosts.top(activityTopValues), agents.top(activityTopValues), referers.top(activityTopValues)
	a.Countries, a.Signatures, a.Sensitive = countries.top(activityTopValues), sigs.top(len(sigs)), sens.top(len(sens))
	return a
}

func flagged(g model.ActivityGroup) bool { return len(g.Signatures) > 0 || g.Sensitive != "" }

// cleanReferer — Referer без параметров и якоря (в них бывают токены и персональные данные).
func cleanReferer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		u.RawQuery, u.Fragment, u.User = "", "", nil
		s = u.String()
	} else {
		s, _, _ = strings.Cut(s, "?")
	}
	return edgeService.Clean(s, edgeService.MaxRefererLen)
}
