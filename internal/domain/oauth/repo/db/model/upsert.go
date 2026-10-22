package model

import (
	domainModel "github.com/rendau/pulse/internal/domain/oauth/model"
)

type GrantUpsert struct {
	PKId string

	KeyHash       string
	ClientId      string
	ClientName    string
	RedirectUri   string
	CodeChallenge string
}

func (m *GrantUpsert) CreateColumnMap() map[string]any {
	return map[string]any{
		"key_hash":       m.KeyHash,
		"client_id":      m.ClientId,
		"client_name":    m.ClientName,
		"redirect_uri":   m.RedirectUri,
		"code_challenge": m.CodeChallenge,
	}
}

func (m *GrantUpsert) ReturningColumnMap() map[string]any {
	return map[string]any{"id": &m.PKId}
}

func (m *GrantUpsert) PKColumnMap() map[string]any {
	return map[string]any{"id": m.PKId}
}

func DecodeGrantUpsert(v *domainModel.Grant) *GrantUpsert {
	return &GrantUpsert{
		KeyHash:       v.KeyHash,
		ClientId:      v.ClientId,
		ClientName:    v.ClientName,
		RedirectUri:   v.RedirectUri,
		CodeChallenge: v.CodeChallenge,
	}
}
