package service

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rendau/pulse/internal/domain/oauth/model"
)

const (
	// clientIdPrefix — client_id, выданные pulse.
	clientIdPrefix = "pscl_"

	clientNonceBytes = 9
)

// ClientCodec выдаёт client_id без хранения: данные клиента упакованы в id и подписаны, поэтому
// регистрация (открытый адрес без аутентификации) ничего не пишет в БД.
type ClientCodec struct {
	key []byte
}

type clientPayload struct {
	// Nonce делает каждую регистрацию отдельным клиентом даже с теми же данными: токен привязан
	// к регистрации, которая его получила.
	Nonce        string   `json:"i"`
	Name         string   `json:"n"`
	RedirectUris []string `json:"r"`
	AuthMethod   string   `json:"m"`
}

// NewClientCodec выводит ключ подписи из secret (MCP_OAUTH_SECRET), сам секрет как есть
// не используется.
func NewClientCodec(secret string) *ClientCodec {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("pulse oauth client"))
	return &ClientCodec{key: mac.Sum(nil)}
}

func (c *ClientCodec) Encode(client *model.Client) (string, error) {
	nonce := make([]byte, clientNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("rand.Read: %w", err)
	}

	raw, err := json.Marshal(&clientPayload{
		Nonce:        base64.RawURLEncoding.EncodeToString(nonce),
		Name:         client.Name,
		RedirectUris: client.RedirectUris,
		AuthMethod:   client.AuthMethod,
	})
	if err != nil {
		return "", fmt.Errorf("json.Marshal: %w", err)
	}

	payload := base64.RawURLEncoding.EncodeToString(raw)

	return clientIdPrefix + payload + "." + c.sign("id:"+payload), nil
}

func (c *ClientCodec) Decode(clientId string) (*model.Client, bool) {
	rest, ok := strings.CutPrefix(clientId, clientIdPrefix)
	if !ok {
		return nil, false
	}
	payload, sig, ok := strings.Cut(rest, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(c.sign("id:"+payload))) {
		return nil, false
	}

	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return nil, false
	}
	p := &clientPayload{}
	if err = json.Unmarshal(raw, p); err != nil {
		return nil, false
	}

	return &model.Client{
		Name:         p.Name,
		RedirectUris: p.RedirectUris,
		AuthMethod:   p.AuthMethod,
	}, true
}

// Secret — секрет конфиденциального клиента, выводится из его id.
func (c *ClientCodec) Secret(clientId string) string {
	return c.sign("secret:" + clientId)
}

func (c *ClientCodec) VerifySecret(clientId, secret string) bool {
	return secret != "" && hmac.Equal([]byte(secret), []byte(c.Secret(clientId)))
}

func (c *ClientCodec) sign(value string) string {
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
