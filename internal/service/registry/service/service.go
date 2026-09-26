package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rendau/pulse/internal/errs"
	"github.com/rendau/pulse/internal/infra/httpx"
	"github.com/rendau/pulse/internal/util/imageref"
)

const (
	manifestAccept = "application/vnd.oci.image.index.v1+json, " +
		"application/vnd.oci.image.manifest.v1+json, " +
		"application/vnd.docker.distribution.manifest.list.v2+json, " +
		"application/vnd.docker.distribution.manifest.v2+json"

	maxBodyBytes  = 4 << 20
	tokenTTL      = 4 * time.Minute
	preferredOS   = "linux"
	preferredArch = "amd64"
)

// Service — клиент registry с bearer-авторизацией по challenge (WWW-Authenticate).
type Service struct {
	httpClient *http.Client
	// credentials: host → password для basic-auth на token endpoint (для ghcr.io — PAT)
	credentials map[string]string

	labelsCache sync.Map // digestKey → map[string]string
	tokenMu     sync.Mutex
	tokens      map[string]cachedToken // host+path → token
}

type cachedToken struct {
	token     string
	expiresAt time.Time
}

func New(credentials map[string]string) *Service {
	return &Service{
		httpClient:  httpx.New(httpx.Config{Timeout: 20 * time.Second, VerifyTLS: true}),
		credentials: credentials,
		tokens:      make(map[string]cachedToken),
	}
}

func (s *Service) Ping(ctx context.Context, host string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/v2/", nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("%s: unexpected status %d", host, resp.StatusCode)
	}

	return nil
}

func (s *Service) GetImageLabels(ctx context.Context, ref string) (map[string]string, error) {
	imageRef, err := imageref.Parse(ref)
	if err != nil {
		return nil, err
	}

	cacheKey := imageRef.String()
	if imageRef.Digest != "" {
		if cached, ok := s.labelsCache.Load(cacheKey); ok {
			return cached.(map[string]string), nil
		}
	}

	manifest, err := s.getManifest(ctx, imageRef, imageRef.Reference())
	if err != nil {
		return nil, err
	}

	// index / manifest list → выбираем платформу и идём за конкретным манифестом
	if len(manifest.Manifests) > 0 {
		childDigest := pickPlatform(manifest.Manifests)
		manifest, err = s.getManifest(ctx, imageRef, childDigest)
		if err != nil {
			return nil, err
		}
	}

	if manifest.Config.Digest == "" {
		return nil, fmt.Errorf("%s: manifest without config digest", ref)
	}

	var imageConfig imageConfigRep
	if err = s.sendRequest(ctx, imageRef, "blobs/"+manifest.Config.Digest, "", &imageConfig); err != nil {
		return nil, err
	}

	labels := imageConfig.Config.Labels
	if labels == nil {
		labels = map[string]string{}
	}

	if imageRef.Digest != "" {
		s.labelsCache.Store(cacheKey, labels)
	}

	return labels, nil
}

func (s *Service) getManifest(ctx context.Context, imageRef imageref.Ref, reference string) (*manifestRep, error) {
	manifest := &manifestRep{}
	if err := s.sendRequest(ctx, imageRef, "manifests/"+reference, manifestAccept, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

// pickPlatform выбирает манифест под linux/amd64, иначе первый попавшийся.
func pickPlatform(manifests []manifestDescriptor) string {
	for _, m := range manifests {
		if m.Platform.OS == preferredOS && m.Platform.Architecture == preferredArch {
			return m.Digest
		}
	}
	return manifests[0].Digest
}

// sendRequest — единственная точка отправки: GET /v2/<path>/<endpoint> с bearer-токеном,
// при 401 — получение токена по challenge и один повтор.
func (s *Service) sendRequest(ctx context.Context, imageRef imageref.Ref, endpoint, accept string, repObj any) error {
	uri := "https://" + imageRef.Host + "/v2/" + imageRef.Path + "/" + endpoint
	tokenKey := imageRef.Host + "/" + imageRef.Path

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if token := s.cachedToken(tokenKey); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("%w: %s: %w", errs.ServiceNA, uri, err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("read body: %w", err)
		}

		switch {
		case resp.StatusCode == http.StatusUnauthorized && attempt == 0:
			if err = s.authorize(ctx, imageRef.Host, tokenKey, resp.Header.Get("WWW-Authenticate")); err != nil {
				return err
			}
			continue
		case resp.StatusCode == http.StatusNotFound:
			return fmt.Errorf("%w: %s", errs.ObjectNotFound, uri)
		case resp.StatusCode < 200 || resp.StatusCode > 299:
			return fmt.Errorf("%w: %s: status %d", errs.ServiceNA, uri, resp.StatusCode)
		}

		if err = json.Unmarshal(body, repObj); err != nil {
			return fmt.Errorf("decode %s: %w", uri, err)
		}
		return nil
	}

	return fmt.Errorf("%w: %s: unauthorized", errs.NotAuthorized, uri)
}

func (s *Service) cachedToken(key string) string {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()

	cached, ok := s.tokens[key]
	if !ok || time.Now().After(cached.expiresAt) {
		return ""
	}
	return cached.token
}

// authorize обменивает challenge «Bearer realm=…,service=…,scope=…» на токен.
func (s *Service) authorize(ctx context.Context, host, tokenKey, challenge string) error {
	params := parseChallenge(challenge)
	realm := params["realm"]
	if realm == "" {
		return fmt.Errorf("%w: %s: no bearer realm in challenge", errs.NotAuthorized, host)
	}

	query := url.Values{}
	if v := params["service"]; v != "" {
		query.Set("service", v)
	}
	if v := params["scope"]; v != "" {
		query.Set("scope", v)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("new token request: %w", err)
	}
	if password := s.credentials[host]; password != "" {
		req.SetBasicAuth("token", password)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: token endpoint %s: %w", errs.ServiceNA, realm, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("read token body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: token endpoint %s: status %d", errs.NotAuthorized, realm, resp.StatusCode)
	}

	var tokenRep tokenRep
	if err = json.Unmarshal(body, &tokenRep); err != nil {
		return fmt.Errorf("decode token: %w", err)
	}

	token := tokenRep.Token
	if token == "" {
		token = tokenRep.AccessToken
	}
	if token == "" {
		return fmt.Errorf("%w: token endpoint %s: empty token", errs.NotAuthorized, realm)
	}

	s.tokenMu.Lock()
	s.tokens[tokenKey] = cachedToken{token: token, expiresAt: time.Now().Add(tokenTTL)}
	s.tokenMu.Unlock()

	return nil
}

// parseChallenge разбирает `Bearer realm="…",service="…",scope="…"` в карту.
func parseChallenge(header string) map[string]string {
	result := make(map[string]string, 3)

	header = strings.TrimSpace(header)
	if scheme, rest, ok := strings.Cut(header, " "); ok && strings.EqualFold(scheme, "Bearer") {
		header = rest
	} else {
		return result
	}

	for _, part := range strings.Split(header, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		result[strings.ToLower(key)] = strings.Trim(value, `"`)
	}

	return result
}

// transport models

type manifestRep struct {
	Config    manifestDescriptor   `json:"config"`
	Manifests []manifestDescriptor `json:"manifests"`
}

type manifestDescriptor struct {
	Digest   string `json:"digest"`
	Platform struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

type imageConfigRep struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"config"`
}

type tokenRep struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}
