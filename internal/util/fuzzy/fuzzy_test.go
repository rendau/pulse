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
