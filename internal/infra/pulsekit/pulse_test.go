package pulsekit

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	indexerModel "github.com/mechta-market/pulse/internal/service/indexer/service/model"
)

// Только в pulse (в сервисы не копируется): манифест, который строит pulsekit, проходит
// проверку pulse без замечаний.
func TestManifest_PassesPulseValidation(t *testing.T) {
	raw, err := json.Marshal(newKit().Manifest())
	require.NoError(t, err)

	parsed, err := indexerModel.ParseManifest(raw)
	require.NoError(t, err)
	assert.Empty(t, parsed.Problems)
	require.Len(t, parsed.Metadata.Endpoints, 1)
	endpoint := parsed.Metadata.Endpoints[0]
	response := endpoint.Response
	assert.Equal(t, "phone", response.Properties["customer_phone"].Personal)
	assert.Equal(t, 30, response.Properties["stuck_reason"].MaxLength)
	assert.Equal(t, 2, response.Properties["history"].MaxItems)
	assert.Equal(t, "date-time", response.Properties["history"].Items.Properties["at"].Format)
	assert.Equal(t, "integer", response.Properties["by_state"].Values.Type)
	assert.Equal(t, []string{"new", "paid", "shipped"}, response.Properties["status"].Enum)
	assert.Equal(t, "статус заказа: new, paid или shipped", response.Properties["status"].Description, "описание с запятыми — целиком")
	assert.NotContains(t, response.Properties, "internal")
	assert.Equal(t, 20, endpoint.MaxRows)
	assert.Equal(t, "20", endpoint.Params["limit"].Default)
	require.Len(t, parsed.Metadata.Dependencies, 2)
	assert.Equal(t, "приём и выдача заказов", parsed.Metadata.Dependencies[0].Affects)
	assert.Len(t, parsed.Metadata.Metrics, 1)
	assert.Len(t, parsed.Metadata.Logs.ErrorPatterns, 1)
	assert.Len(t, parsed.Metadata.Runbooks, 1)
	assert.Equal(t, "9f597a7c1e2d4b8a0f3c6e5d7b9a1c2e4f6a8b0c", parsed.Commit)
	require.NotNil(t, parsed.Metadata.Domain)
	assert.Equal(t, 2*time.Hour, parsed.Metadata.Domain.Entities[0].Statuses[0].StuckAfter)
	assert.Equal(t, "order_status", parsed.Metadata.Domain.Questions[0].Endpoint)

	assert.Contains(t, string(raw), `"default":20`, "default числового параметра — числом")
}
