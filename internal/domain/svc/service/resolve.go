package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samber/lo"

	"github.com/mechta-market/pulse/internal/constant"
	"github.com/mechta-market/pulse/internal/domain/svc/model"
	"github.com/mechta-market/pulse/internal/util/fuzzy"
)

const (
	ResolveMaxCandidates = 5
	// ResolveAmbiguousThreshold — уверенность лидера ниже порога → агент переспрашивает.
	ResolveAmbiguousThreshold = 0.5

	minTokenLen        = 3
	fuzzyMinSimilarity = 0.65
)

// Resolve переводит формулировку в кандидатов каталога. Обычный код, не LLM:
// точное имя → алиас → имя в кластере → title → подстрока → fuzzy по имени/алиасам/именам в
// кластере/title → подстрока в description.
// Запрос из нескольких слов сопоставляется и целиком, и по отдельным словам.
func (s *Service) Resolve(ctx context.Context, query string) ([]*model.Candidate, error) {
	services, _, err := s.repoDb.List(ctx, &model.ListReq{})
	if err != nil {
		return nil, fmt.Errorf("repoDb.List: %w", err)
	}

	return Rank(services, query), nil
}

// Rank оценивает сервисы против запроса и возвращает до ResolveMaxCandidates лучших.
func Rank(services []*model.Main, query string) []*model.Candidate {
	whole := fuzzy.Normalize(query)
	if whole == "" {
		return nil
	}

	probes := lo.Uniq(append([]string{whole}, fuzzy.Tokens(query, minTokenLen)...))

	candidates := lo.FilterMap(services, func(svc *model.Main, _ int) (*model.Candidate, bool) {
		best := model.Candidate{Service: svc}
		for _, probe := range probes {
			score, matchedBy := scoreService(svc, probe)
			if score > best.Confidence {
				best.Confidence, best.MatchedBy = score, matchedBy
			}
		}
		return &best, best.Confidence > 0
	})

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Confidence != candidates[j].Confidence {
			return candidates[i].Confidence > candidates[j].Confidence
		}
		return candidates[i].Service.Name < candidates[j].Service.Name
	})

	if len(candidates) > ResolveMaxCandidates {
		candidates = candidates[:ResolveMaxCandidates]
	}

	return candidates
}

// Ambiguous — нужно ли агенту переспросить пользователя.
func Ambiguous(candidates []*model.Candidate) bool {
	return len(candidates) == 0 || candidates[0].Confidence < ResolveAmbiguousThreshold
}

func scoreService(svc *model.Main, probe string) (float64, string) {
	name := fuzzy.Normalize(svc.Name)
	title := fuzzy.Normalize(svc.Title)
	aliases := lo.Map(svc.Aliases, func(a string, _ int) string { return fuzzy.Normalize(a) })
	clusterNames := lo.Map(svc.ClusterNames, func(a string, _ int) string { return fuzzy.Normalize(a) })

	// точные совпадения
	if probe == name {
		return 1.0, constant.MatchedByName
	}
	if lo.Contains(aliases, probe) {
		return 0.95, constant.MatchedByAlias
	}
	if lo.Contains(clusterNames, probe) {
		return clusterNameExact, constant.MatchedByClusterName
	}
	if title != "" && probe == title {
		return 0.9, constant.MatchedByTitle
	}

	// подстроки (в обе стороны: «payments» ⊂ «payments-api», «payments-api-v2» ⊃ «payments-api»)
	if len(probe) >= minTokenLen && (strings.Contains(name, probe) || strings.Contains(probe, name)) {
		return 0.8, constant.MatchedByName
	}
	for _, alias := range aliases {
		if len(alias) >= minTokenLen && (strings.Contains(alias, probe) || strings.Contains(probe, alias)) {
			return 0.75, constant.MatchedByAlias
		}
	}
	for _, clusterName := range clusterNames {
		if len(clusterName) >= minTokenLen && (strings.Contains(clusterName, probe) || strings.Contains(probe, clusterName)) {
			return 0.72, constant.MatchedByClusterName
		}
	}
	if title != "" && strings.Contains(title, probe) {
		return 0.7, constant.MatchedByTitle
	}

	// fuzzy: опечатки и вариации написания
	bestSim := fuzzy.Similarity(probe, name)
	for _, candidate := range append(append(aliases, clusterNames...), title) {
		if candidate != "" {
			bestSim = max(bestSim, fuzzy.Similarity(probe, candidate))
		}
	}
	if bestSim >= fuzzyMinSimilarity {
		return 0.4 + bestSim*0.45, constant.MatchedByFuzzy
	}

	// описание — самый слабый сигнал
	if svc.Description != "" && strings.Contains(strings.ToLower(svc.Description), probe) {
		return 0.5, constant.MatchedByDescription
	}

	return 0, ""
}
