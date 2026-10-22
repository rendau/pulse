// Package oauth — сервер авторизации OAuth 2.1 MCP-сервера pulse: MCP-клиенты с коннекторами
// (приложения Claude) подключаются по URL. Пользователей у pulse нет, поэтому подключение
// разрешает человек, знающий ключ доступа (один из MCP_EXTERNAL_TOKENS): он вводит ключ на
// странице согласия, клиент получает токены, которые действуют как этот ключ.
package oauth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"

	"github.com/rendau/pulse/internal/domain/oauth/model"
	oauthService "github.com/rendau/pulse/internal/domain/oauth/service"
	"github.com/rendau/pulse/internal/errs"
)

const (
	// Scope — единственный scope: доступ к MCP-серверу.
	Scope = "mcp"

	codeTTL = 2 * time.Minute

	// pendingGrantTTL: подключение без токенов старше этого — брошенное согласие или истёкшее
	// подключение.
	pendingGrantTTL = time.Hour

	// touchInterval ограничивает запись last_used_at: MCP-клиент ходит на каждый инструмент.
	touchInterval = time.Minute
)

type Config struct {
	Enabled bool
	// Issuer — публичный адрес MCP-сервера: issuer OAuth и защищаемый ресурс сразу.
	Issuer string
	// Keys — ключи доступа, которыми можно разрешить подключение (MCP_EXTERNAL_TOKENS).
	Keys         []string
	RedirectUris []string
	AccessTTL    time.Duration
	RefreshTTL   time.Duration
}

// Access — подключение, которым аутентифицирован запрос MCP.
type Access struct {
	GrantId    string
	ClientName string
	ExpiresAt  time.Time
}

type Usecase struct {
	conf        Config
	keyHashes   [][]byte
	svc         ServiceI
	clientCodec ClientCodecI
	txm         TransactionManagerI

	touchMu   sync.Mutex
	lastTouch map[string]time.Time

	wg sync.WaitGroup
}

func New(conf Config, svc ServiceI, clientCodec ClientCodecI, txm TransactionManagerI) *Usecase {
	return &Usecase{
		conf: conf,
		keyHashes: lo.FilterMap(conf.Keys, func(key string, _ int) ([]byte, bool) {
			key = strings.TrimSpace(key)
			return []byte(oauthService.HashToken(key)), key != ""
		}),
		svc:         svc,
		clientCodec: clientCodec,
		txm:         txm,
		lastTouch:   map[string]time.Time{},
	}
}

func (u *Usecase) Config() Config {
	return u.conf
}

// AccessFromToken аутентифицирует запрос MCP по access-токену. Ключ, которым разрешено
// подключение, проверяется по текущей конфигурации на каждом запросе.
func (u *Usecase) AccessFromToken(ctx context.Context, token string) (*Access, error) {
	if !u.conf.Enabled {
		return nil, errs.NotAuthorized
	}

	item, found, err := u.svc.FindToken(ctx, token, model.TokenKindAccess)
	if err != nil {
		return nil, fmt.Errorf("svc.FindToken: %w", err)
	}
	if !found {
		u.logRejectedToken(ctx, token)
		return nil, errs.NotAuthorized
	}

	grant, found, err := u.svc.GetGrant(ctx, item.GrantId, false)
	if err != nil {
		return nil, fmt.Errorf("svc.GetGrant: %w", err)
	}
	if !found {
		slog.Warn("oauth: access token rejected", "reason", "connection removed", "grant_id", item.GrantId)
		return nil, errs.NotAuthorized
	}
	if !u.keyHashAllowed(grant.KeyHash) {
		slog.Warn("oauth: access token rejected", "reason", "access key of the connection removed from MCP_EXTERNAL_TOKENS",
			"grant_id", grant.Id, "client_name", grant.ClientName)
		return nil, errs.NotAuthorized
	}

	u.touchLastUsed(ctx, grant.Id)

	return &Access{GrantId: grant.Id, ClientName: grant.ClientName, ExpiresAt: item.ExpiresAt}, nil
}

// logRejectedToken объясняет, почему предъявленный access-токен не живой: по журналу запросов
// устаревший токен от чужого не отличить.
func (u *Usecase) logRejectedToken(ctx context.Context, token string) {
	item, found, err := u.svc.FindAnyToken(ctx, token)
	switch {
	case err != nil:
		slog.Warn("oauth: access token rejected", "reason", "lookup failed", "error", err)
	case !found:
		slog.Warn("oauth: access token rejected", "reason", "unknown token")
	case item.Kind != model.TokenKindAccess:
		slog.Warn("oauth: access token rejected", "reason", "not an access token", "kind", item.Kind, "grant_id", item.GrantId)
	default:
		slog.Warn("oauth: access token rejected", "reason", "expired", "expires_at", item.ExpiresAt, "grant_id", item.GrantId)
	}
}

// Start запускает фоновую чистку: сразу и затем раз в interval, до отмены ctx.
func (u *Usecase) Start(ctx context.Context, interval time.Duration) {
	u.wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			u.Cleanup(ctx)

			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

func (u *Usecase) Wait() {
	u.wg.Wait()
}

// Cleanup удаляет истёкшие токены и подключения, оставшиеся без токенов.
func (u *Usecase) Cleanup(ctx context.Context) {
	tokens, grants, err := u.svc.Cleanup(ctx, time.Now().Add(-pendingGrantTTL))
	if err != nil {
		slog.Error("oauth: cleanup", "error", err)
		return
	}
	if grants > 0 {
		slog.Info("oauth: cleanup", "tokens", tokens, "grants", grants)
	}
}

// keyAllowed — один ли это из ключей доступа; сравнение со всеми за постоянное время.
func (u *Usecase) keyAllowed(key string) bool {
	key = strings.TrimSpace(key)
	return key != "" && u.keyHashAllowed(oauthService.HashToken(key))
}

func (u *Usecase) keyHashAllowed(hash string) bool {
	match := 0
	for _, h := range u.keyHashes {
		match |= subtle.ConstantTimeCompare([]byte(hash), h)
	}
	return match == 1
}

func (u *Usecase) touchLastUsed(ctx context.Context, id string) {
	u.touchMu.Lock()
	if last, ok := u.lastTouch[id]; ok && time.Since(last) < touchInterval {
		u.touchMu.Unlock()
		return
	}
	u.lastTouch[id] = time.Now()
	u.touchMu.Unlock()

	if err := u.svc.TouchGrantLastUsed(ctx, id); err != nil {
		slog.Warn("oauth: touch last_used_at", "error", err, "id", id)
	}
}
