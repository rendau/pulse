package fuzzy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalize(t *testing.T) {
	assert.Equal(t, "kafka-producer", Normalize("Kafka_Producer"))
	assert.Equal(t, "приём-платежей", Normalize("  Приём платежей "))
}

func TestTokens(t *testing.T) {
	assert.Equal(t, []string{"почему", "проходят", "платежи"}, Tokens("Почему не проходят платежи?", 3))
	assert.Equal(t, []string{"kafka", "producer"}, Tokens("kafka-producer", 3))
}

func TestSimilarity(t *testing.T) {
	assert.Equal(t, 1.0, Similarity("abc", "abc"))
	assert.InDelta(t, 0.92, Similarity("payments-api", "payment-api"), 0.02)
	assert.Less(t, Similarity("payments", "delivery"), 0.5)
	assert.Equal(t, 1.0, Similarity("", ""))
}

func TestTranslit(t *testing.T) {
	assert.Nil(t, Translit("caravan"))
	assert.Contains(t, Translit("Караван"), "karavan")
	assert.Contains(t, Translit("караван"), "caravan")
	assert.Equal(t, "seller", Translit("селлер")[0])
	assert.Equal(t, "planora", Translit("планора")[0])
	assert.Contains(t, Translit("нотифаер-смс"), "notifaer-sms")
	assert.Contains(t, Translit("қазпочта"), "qazpochta")
	assert.LessOrEqual(t, len(Translit("кхцйыжюяёвщ")), maxTranslitVariants)
}
