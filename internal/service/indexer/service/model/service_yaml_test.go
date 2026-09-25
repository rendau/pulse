package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcModel "github.com/mechta-market/pulse/internal/domain/svc/model"
)

const sample = `
name: payments-api
title: Приём платежей
aliases: [платежи, оплата, эквайринг, payments]
owner:
  team: team-billing
  contacts: ["@i.petrov", "billing@company.kz"]
criticality: high
description: >
  Принимает платежи от фронта, ходит в эквайринг, пишет в billing-core.

metrics:
  - id: success_rate
    title: Доля успешных платежей
    promql: 'sum(rate(payments_completed_total[5m])) / sum(rate(payments_started_total[5m]))'
    unit: ratio
    direction: higher_is_better

logs:
  selector: '{app="payments-api"}'
  error_patterns:
    - name: acquirer_timeout
      pattern: "acquirer.*timeout"

runbooks:
  - title: Платежи не проходят
    url: https://wiki/runbook

endpoints:
  - id: stuck_queue_items
    title: Записи в очереди
    path: /internal/diagnostics/stuck
    method: GET
    params:
      older_than_minutes: { type: int, default: 30, max: 1440 }
    max_rows: 50
    pii: []
    timeout: 5s
  - id: order_details
    title: Детали заказа
    path: /internal/diagnostics/order/{id}
    method: GET
    params: { id: { type: string } }
    pii: [customer_name, phone]
    timeout: 5s
`

func TestParseAndDecodeServiceYaml(t *testing.T) {
	parsed, err := ParseServiceYaml([]byte(sample))
	require.NoError(t, err)

	edit := DecodeServiceYaml(parsed)
	assert.Equal(t, "payments-api", *edit.Name)
	assert.Equal(t, "Приём платежей", *edit.Title)
	assert.Equal(t, "team-billing", *edit.OwnerTeam)
	assert.Equal(t, []string{"@i.petrov", "billing@company.kz"}, *edit.OwnerContacts)
	assert.Equal(t, []string{"платежи", "оплата", "эквайринг", "payments"}, *edit.Aliases)
	assert.Equal(t, "high", *edit.Criticality)
	assert.Contains(t, *edit.Description, "billing-core")
	assert.True(t, *edit.MetadataPresent)

	meta := *edit.Metadata
	require.Len(t, meta.Metrics, 1)
	assert.Equal(t, "success_rate", meta.Metrics[0].Id)
	assert.Equal(t, "higher_is_better", meta.Metrics[0].Direction)
	assert.Equal(t, `{app="payments-api"}`, meta.Logs.Selector)
	require.Len(t, meta.Logs.ErrorPatterns, 1)
	require.Len(t, meta.Runbooks, 1)

	assert.Equal(t, svcModel.MetadataSourceServiceYaml, meta.Source)
	assert.Empty(t, meta.Endpoints, "диагностические ручки — только из манифеста сервиса")
}

func TestParseServiceYaml_Invalid(t *testing.T) {
	_, err := ParseServiceYaml([]byte("title: no name"))
	assert.Error(t, err)

	_, err = ParseServiceYaml([]byte("name: [broken"))
	assert.Error(t, err)
}
