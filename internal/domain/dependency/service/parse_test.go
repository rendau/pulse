package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/domain/dependency/model"
)

func TestParseEndpoints(t *testing.T) {
	s := New(nil)
	cases := map[string][]model.Endpoint{
		"http://acquirer-gateway.prod.svc:8080":        {{Scheme: "http", Host: "acquirer-gateway.prod.svc", Port: 8080}},
		"postgres://user:pass@billing-db:5432/billing": {{Scheme: "postgres", Host: "billing-db", Port: 5432}},
		"redis://cache-redis":                          {{Scheme: "redis", Host: "cache-redis", Port: 6379}},
		"kafka-0.kafka:9092, kafka-1.kafka:9092":       {{Host: "kafka-0.kafka", Port: 9092}, {Host: "kafka-1.kafka", Port: 9092}},
		"billing-core.prod.svc.cluster.local:9090":     {{Host: "billing-core.prod.svc.cluster.local", Port: 9090}},
		"https://api.epay.kz/v2":                       {{Scheme: "https", Host: "api.epay.kz", Port: 443}},
		"grpc.acquirer.example.com":                    {{Host: "grpc.acquirer.example.com"}},
		"10.42.0.7:6379":                               {{Host: "10.42.0.7", Port: 6379}},
		"true":                                         nil, "5m": nil, "info": nil, "1.2.3": nil, "orders.created": nil, "": nil,
		"localhost:8080": nil, "http://127.0.0.1:3003/healthcheck": nil,
	}
	for in, want := range cases {
		got := s.ParseEndpoints(in)
		if want == nil {
			assert.Empty(t, got, in)
			continue
		}
		assert.Equal(t, want, got, in)
	}

	got := s.ParseEndpoints("postgres://user:s3cr3t@billing-db:5432/billing")
	require.Len(t, got, 1)
	assert.NotContains(t, got[0].Host, "s3cr3t", "учётные данные не сохраняются")
}

func TestClusterHost(t *testing.T) {
	s := New(nil)
	cases := map[string][3]any{
		"acquirer-gateway":                        {"acquirer-gateway", "", true},
		"acquirer-gateway.prod":                   {"acquirer-gateway", "prod", true},
		"acquirer-gateway.prod.svc":               {"acquirer-gateway", "prod", true},
		"acquirer-gateway.prod.svc.cluster.local": {"acquirer-gateway", "prod", true},
		"api.epay.kz":                             {"", "", false},
		"10.42.0.7":                               {"", "", false},
	}
	for in, want := range cases {
		name, ns, ok := s.ClusterHost(in)
		assert.Equal(t, want[2], ok, in)
		assert.Equal(t, want[0], name, in)
		assert.Equal(t, want[1], ns, in)
	}
}
