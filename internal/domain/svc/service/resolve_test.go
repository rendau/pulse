package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/constant"
	"github.com/mechta-market/pulse/internal/domain/svc/model"
)

func catalog() []*model.Main {
	return []*model.Main{
		{Name: "payments-api", Title: "Приём платежей", Aliases: []string{"платежи", "оплата", "эквайринг", "payments"},
			Description: "Принимает платежи от фронта, ходит в эквайринг, пишет в billing-core."},
		{Name: "acquirer-gateway", Title: "Шлюз эквайринга", Description: "Проксирует запросы в банки-эквайеры."},
		{Name: "kafka_producer", Title: "Kafka producer"},
		{Name: "delivery", Title: "Доставка", Aliases: []string{"доставка", "курьеры"}},
		{Name: "mechta-site", Title: "Сайт mechta.kz", Aliases: []string{"сайт"}},
		{Name: "kusec-pg"},
		{Name: "orders-center", ClusterNames: []string{"ocenter"}},
	}
}

func first(t *testing.T, query string) *model.Candidate {
	t.Helper()
	candidates := Rank(catalog(), query)
	require.NotEmpty(t, candidates, "query %q", query)
	return candidates[0]
}

func TestRank_ExactAndAlias(t *testing.T) {
	c := first(t, "payments-api")
	assert.Equal(t, "payments-api", c.Service.Name)
	assert.Equal(t, 1.0, c.Confidence)
	assert.Equal(t, constant.MatchedByName, c.MatchedBy)

	c = first(t, "Платежи")
	assert.Equal(t, "payments-api", c.Service.Name)
	assert.Equal(t, constant.MatchedByAlias, c.MatchedBy)
	assert.False(t, Ambiguous(Rank(catalog(), "Платежи")))
}

func TestRank_HumanPhrases(t *testing.T) {
	cases := map[string]string{
		"почему не проходят платежи": "payments-api",
		"что с доставкой":            "delivery",
		"kafka producer":             "kafka_producer",
		"kafka-producer":             "kafka_producer",
		"сайт лежит":                 "mechta-site",
		"paymets-api":                "payments-api", // опечатка → fuzzy
		"эквайринг":                  "payments-api", // алиас сильнее description
		"банки-эквайеры":             "acquirer-gateway",
		"Приём платежей":             "payments-api",
	}

	for query, want := range cases {
		c := first(t, query)
		assert.Equal(t, want, c.Service.Name, "query %q matched by %s (%.2f)", query, c.MatchedBy, c.Confidence)
	}
}

func TestRank_LimitsAndAmbiguity(t *testing.T) {
	candidates := Rank(catalog(), "a")
	assert.LessOrEqual(t, len(candidates), ResolveMaxCandidates)

	candidates = Rank(catalog(), "совершенно неизвестная система")
	assert.True(t, Ambiguous(candidates))

	assert.Empty(t, Rank(catalog(), "   "))
}

func TestRank_ClusterNames(t *testing.T) {
	c := first(t, "ocenter")
	assert.Equal(t, "orders-center", c.Service.Name, "имя k8s Service / приложения ruto")
	assert.Equal(t, constant.MatchedByClusterName, c.MatchedBy)
	assert.False(t, Ambiguous(Rank(catalog(), "ocenter")))

	c = first(t, "ocenter api")
	assert.Equal(t, "orders-center", c.Service.Name)
}

// TestRank_ChatCases — случаи из переписки с ботом.
func TestRank_ChatCases(t *testing.T) {
	services := []*model.Main{
		{Name: "caravan"}, {Name: "caravaneer"}, {Name: "seller"}, {Name: "planora"},
		{Name: "notifire", ClusterNames: []string{"notifire"}},
		{Name: "sms", ClusterNames: []string{"notifire-sms", "sms-im", "sms-service"}},
		{Name: "notifire-pg"},
	}

	// кириллица → латиница: «к» пишут и k, и c
	for query, want := range map[string]string{"караван": "caravan", "что с караваном": "caravan", "селлер": "seller", "планора": "planora"} {
		candidates := Rank(services, query)
		require.NotEmpty(t, candidates, query)
		assert.Equal(t, want, candidates[0].Service.Name, query)
		assert.Equal(t, constant.MatchedByTranslit, candidates[0].MatchedBy, query)
		assert.False(t, Ambiguous(candidates), query)
	}

	// имя workload'а целиком сильнее отдельных слов: notifire-sms — это sms, а не notifire
	candidates := Rank(services, "notifire-sms")
	require.NotEmpty(t, candidates)
	assert.Equal(t, "sms", candidates[0].Service.Name)
	assert.Equal(t, constant.MatchedByClusterName, candidates[0].MatchedBy)
	assert.False(t, Ambiguous(candidates))

	// два почти равных кандидата — переспросить
	assert.True(t, Ambiguous([]*model.Candidate{{Confidence: 0.85}, {Confidence: 0.84}}))
	assert.False(t, Ambiguous([]*model.Candidate{{Confidence: 0.93}, {Confidence: 0.85}}))
}
