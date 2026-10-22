package model

import "time"

const (
	TokenKindCode    = "code"
	TokenKindAccess  = "access"
	TokenKindRefresh = "refresh"
)

const (
	ClientAuthNone        = "none"
	ClientAuthSecretPost  = "client_secret_post"
	ClientAuthSecretBasic = "client_secret_basic"
)

// Client — динамически зарегистрированный OAuth-клиент. Не хранится: его несёт client_id,
// подписанный pulse (см. service.ClientCodec).
type Client struct {
	Name         string
	RedirectUris []string
	AuthMethod   string
}

// Grant — подключение OAuth-клиента, разрешённое ключом доступа (одним из MCP_EXTERNAL_TOKENS).
// KeyHash — sha256 этого ключа: убрали ключ из конфигурации — подключение перестаёт работать.
type Grant struct {
	Id            string
	KeyHash       string
	ClientId      string
	ClientName    string
	RedirectUri   string
	CodeChallenge string
	CreatedAt     time.Time
	ConnectedAt   *time.Time
	LastUsedAt    *time.Time
}

// Token — код авторизации, access- или refresh-токен подключения. Hash — sha256 токена,
// сам токен не хранится.
type Token struct {
	Hash      string
	GrantId   string
	Kind      string
	ExpiresAt time.Time
}
