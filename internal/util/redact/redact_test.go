package redact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// критерий приёмки фазы 4: ни один секрет из набора не проходит в вывод
func TestValue_SecretsNeverLeak(t *testing.T) {
	secrets := map[string]string{
		"DB_PASSWORD":         "s3cr3t",
		"PG_DSN":              "postgres://user:pass@db:5432/app",
		"JWT_SECRET":          "eyJhbGciOiJIUzI1NiJ9",
		"GITHUB_TOKEN":        "ghp_abcdef",
		"API_KEY":             "AKIAIOSFODNN7EXAMPLE",
		"AWS_CREDENTIALS":     "x",
		"private_key":         "-----BEGIN RSA PRIVATE KEY-----",
		"smtp_pass":           "hunter2",
		"OAUTH_CLIENT_SECRET": "abc",
		// имя невинное, значение похоже на секрет — allowlist не пропускает
		"CONFIG": "postgres://user:pass@db:5432/app",
		"HEADER": "Basic dXNlcjpwYXNz",
		"NOTE":   "some free text with spaces",
		"CONN":   "amqp://guest:guest@rabbit:5672/",
	}
	for key, value := range secrets {
		got := Value(key, value)
		assert.Equal(t, Mask, got, "%s=%s", key, value)
		assert.NotContains(t, got, value)
	}
	assert.Equal(t, Mask, Secret())
}

func TestValue_AllowlistPasses(t *testing.T) {
	cases := map[[2]string]string{
		{"ACQUIRER_URL", "http://acquirer-gateway.prod.svc:8080"}:    "http://acquirer-gateway.prod.svc:8080",
		{"BILLING_ADDR", "billing-core.prod.svc.cluster.local:9090"}: "billing-core.prod.svc.cluster.local:9090",
		{"KAFKA_BROKERS", "kafka-0:9092,kafka-1:9092"}:               "kafka-0:9092,kafka-1:9092",
		{"HTTP_CORS", "true"}:       "true",
		{"MAX_CONNS", "20"}:         "20",
		{"TIMEOUT", "15s"}:          "15s",
		{"LOG_LEVEL", "info"}:       "info",
		{"MEMORY_LIMIT", "512Mi"}:   "512Mi",
		{"TOPIC", "orders.created"}: "orders.created",
		{"EMPTY", ""}:               "",
	}
	for kv, want := range cases {
		assert.Equal(t, want, Value(kv[0], kv[1]), kv[0])
	}
}

func TestKeyDenied(t *testing.T) {
	assert.True(t, KeyDenied("DB_PASSWORD"))
	assert.True(t, KeyDenied("secretKey"))
	assert.True(t, KeyDenied("PG_DSN"))
	assert.False(t, KeyDenied("HTTP_PORT"))
	assert.False(t, KeyDenied("ACQUIRER_URL"))
}

func TestFields(t *testing.T) {
	in := map[string]any{
		"id": 1, "customer_name": "Иван", "phone": "+7...",
		"items": []any{map[string]any{"sku": "x", "Phone": "hidden"}},
	}
	out := Fields(in, []string{"customer_name", "phone"}).(map[string]any)
	assert.Equal(t, 1, out["id"])
	assert.Equal(t, Mask, out["customer_name"])
	assert.Equal(t, Mask, out["phone"])
	assert.Equal(t, Mask, out["items"].([]any)[0].(map[string]any)["Phone"])
	assert.Equal(t, "x", out["items"].([]any)[0].(map[string]any)["sku"])
	assert.Equal(t, "Иван", in["customer_name"], "исходник не мутируется")
}
