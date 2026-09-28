package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	svcModel "github.com/rendau/pulse/internal/domain/svc/model"
	usecaseEndpointsModel "github.com/rendau/pulse/internal/usecase/endpoints/model"
)

// Отметка audience — на верхнем уровне ответа call_service_endpoint: по ней агент pulse не
// отдаёт ответ модели (контракт с pulse_agent).
func TestEncodeCallServiceEndpointRep_Audience(t *testing.T) {
	raw, err := json.Marshal(EncodeCallServiceEndpointRep(&usecaseEndpointsModel.CallResult{
		Service: "orders", EndpointId: "order_raw", Audience: svcModel.AudienceHuman, StatusCode: 404, Data: map[string]any{"error": "нет"},
	}))
	require.NoError(t, err)
	var top map[string]any
	require.NoError(t, json.Unmarshal(raw, &top))
	assert.Equal(t, "human", top["audience"])

	raw, err = json.Marshal(EncodeCallServiceEndpointRep(&usecaseEndpointsModel.CallResult{Service: "orders", EndpointId: "order"}))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "audience", "обычная ручка — без отметки")
}
