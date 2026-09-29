package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainModel "github.com/rendau/pulse/internal/domain/svc/model"
)

// Метаданные переживают запись в jsonb и чтение: бизнес-смысл (domain) — целиком.
func TestMetadata_RoundTrip(t *testing.T) {
	in := domainModel.Metadata{
		Source:       domainModel.MetadataSourceManifest,
		Dependencies: []domainModel.Dependency{{Id: "pg", Kind: "postgres", Target: "caravan-pg", Service: "caravan-pg", Critical: true, Affects: "весь сервис"}},
		Endpoints: []domainModel.Endpoint{{
			Id: "delivery_raw", Title: "Доставка как есть", Path: "/diag/delivery/{id}", Audience: domainModel.AudienceHuman,
			Workload: &domainModel.WorkloadRef{Namespace: "default", Kind: "Deployment", Name: "caravan"},
			Params:   map[string]domainModel.EndpointParam{"id": {Type: "string", Pattern: "[0-9]{7}", Required: true}},
			MaxRows:  50, Timeout: 5 * time.Second,
		}},
		Domain: &domainModel.Domain{
			Responsibilities: []string{"Назначает курьера на доставку"},
			NotResponsible:   []domainModel.Boundary{{What: "оплата", Service: "payments"}},
			Entities: []domainModel.Entity{{
				Name: "доставка", IdPattern: "[0-9]{7}", IdExample: "7784512", Description: "доставка заказа",
				Statuses: []domainModel.EntityStatus{{Name: "assigned", Meaning: "курьер назначен", StuckAfter: 30 * time.Minute}},
			}},
			Questions: []domainModel.Question{{Question: "где доставка", Endpoint: "delivery_status"}},
		},
	}

	raw, err := json.Marshal(decodeMetadata(&in))
	require.NoError(t, err)
	var stored metadataJSON
	require.NoError(t, json.Unmarshal(raw, &stored))
	out := encodeMetadata(stored)

	assert.Equal(t, in.Domain, out.Domain)
	assert.Equal(t, in.Dependencies, out.Dependencies)
	assert.Equal(t, in.Endpoints, out.Endpoints, "ручка для человека — с audience и без схемы")

	assert.Nil(t, encodeMetadata(decodeMetadata(&domainModel.Metadata{})).Domain, "не описан — nil")
}
