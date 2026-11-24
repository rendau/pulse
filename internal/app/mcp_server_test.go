package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rendau/pulse/internal/constant"
	handlerMcpP "github.com/rendau/pulse/internal/handler/mcp"
	usecaseFindingModel "github.com/rendau/pulse/internal/usecase/finding/model"
	usecaseSystemP "github.com/rendau/pulse/internal/usecase/system"
)

// pingClient подключается к тестовому серверу и вызывает ping — критерий приёмки фазы 0.
func TestMCPServer_Ping(t *testing.T) {
	ctx := context.Background()

	systemUsecase := usecaseSystemP.New([]usecaseSystemP.Source{
		{Name: "ok_source", Ping: func(context.Context) error { return nil }},
		{Name: "broken_source", Ping: func(context.Context) error { return errors.New("connection refused") }},
		{Name: "disabled_source"},
	})
	handler := handlerMcpP.New(systemUsecase, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	server := MCPServerCreate(handler.Register)

	// токен агента и внешних клиентов; пустые игнорируются
	const token, externalToken = "secret-token", "external-token"
	httpServer := MCPHttpServerCreate("0", token, []string{externalToken, ""}, nil, server, nil)
	ts := httptest.NewServer(httpServer.Handler)
	defer ts.Close()

	t.Run("unauthorized without token", func(t *testing.T) {
		resp, err := http.Post(ts.URL, "application/json", nil)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	for name, bearer := range map[string]string{"wrong token": "nope", "empty token": ""} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+bearer)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}

	t.Run("external token", func(t *testing.T) {
		client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   ts.URL,
			HTTPClient: &http.Client{Transport: &bearerTransport{token: externalToken}},
		}, nil)
		require.NoError(t, err)
		defer func() { _ = session.Close() }()

		_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
		require.NoError(t, err)
	})

	t.Run("tools list and ping call", func(t *testing.T) {
		client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
		transport := &mcp.StreamableClientTransport{
			Endpoint:   ts.URL,
			HTTPClient: &http.Client{Transport: &bearerTransport{token: token}},
		}
		session, err := client.Connect(ctx, transport, nil)
		require.NoError(t, err)
		defer func() { _ = session.Close() }()

		tools, err := session.ListTools(ctx, nil)
		require.NoError(t, err)
		names := make([]string, 0, len(tools.Tools))
		for _, tool := range tools.Tools {
			names = append(names, tool.Name)
		}
		assert.Contains(t, names, "ping")
		assert.Contains(t, names, "get_public_api")
		assert.Contains(t, names, "get_edge_traffic")
		// 13 диагностических + 2 поиска атак по журналу входящих запросов (решение 2026-11-24);
		// у внешних клиентов — ещё report_security_finding (TestMCPServer_ExternalTools)
		assert.LessOrEqual(t, len(names), 15, "не более 15 инструментов")

		// схемы ответов допускают новые поля: клиенты с запомненной схемой не ломаются
		// после деплоя, добавившего поле
		for _, tool := range tools.Tools {
			raw, err := json.Marshal(tool.OutputSchema)
			require.NoError(t, err)
			assert.NotContains(t, string(raw), `"additionalProperties":false`, tool.Name)
		}

		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
		require.NoError(t, err)
		require.False(t, result.IsError)

		raw, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)

		var rep struct {
			Version string `json:"version"`
			Sources []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Error  string `json:"error"`
			} `json:"sources"`
		}
		require.NoError(t, json.Unmarshal(raw, &rep))

		assert.Equal(t, constant.Version, rep.Version)
		require.Len(t, rep.Sources, 3)
		assert.Equal(t, constant.SourceStatusOk, rep.Sources[0].Status)
		assert.Equal(t, constant.SourceStatusError, rep.Sources[1].Status)
		assert.Contains(t, rep.Sources[1].Error, "connection refused")
		assert.Equal(t, constant.SourceStatusDisabled, rep.Sources[2].Status)
	})
}

type bearerTransport struct {
	token string
}

func (t *bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(req)
}

// Право на ответы ручек для человека — только у внутреннего токена (агент pulse).
func TestMCPServer_HumanScope(t *testing.T) {
	ctx := context.Background()

	server := MCPServerCreate(func(server *mcp.Server) {
		mcp.AddTool(server, &mcp.Tool{Name: "whoami"}, func(_ context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, struct {
			Scopes []string `json:"scopes"`
		}, error) {
			var out struct {
				Scopes []string `json:"scopes"`
			}
			if req.Extra != nil && req.Extra.TokenInfo != nil {
				out.Scopes = req.Extra.TokenInfo.Scopes
			}
			return nil, out, nil
		})
	})
	const internalToken, externalToken = "internal-token", "external-token"
	ts := httptest.NewServer(MCPHttpServerCreate("0", internalToken, []string{externalToken}, nil, server, nil).Handler)
	defer ts.Close()

	for token, human := range map[string]bool{internalToken: true, externalToken: false} {
		client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   ts.URL,
			HTTPClient: &http.Client{Transport: &bearerTransport{token: token}},
		}, nil)
		require.NoError(t, err)

		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "whoami"})
		require.NoError(t, err)
		var out struct {
			Scopes []string `json:"scopes"`
		}
		raw, err := json.Marshal(res.StructuredContent)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &out))
		assert.Equal(t, human, slices.Contains(out.Scopes, constant.ScopeHuman), token)
		_ = session.Close()
	}
}

type fakeFinding struct{ calls int }

func (f *fakeFinding) Report(_ context.Context, req *usecaseFindingModel.ReportReq) (*usecaseFindingModel.Report, error) {
	f.calls++
	return &usecaseFindingModel.Report{Key: "security:" + req.Ip, Id: 5}, nil
}

// report_security_finding видят только внешние клиенты: агенту pulse-agent (внутренний токен)
// он не нужен — у агента свой наблюдатель, иначе петля.
func TestMCPServer_ExternalTools(t *testing.T) {
	ctx := context.Background()
	finding := &fakeFinding{}
	handler := handlerMcpP.New(usecaseSystemP.New(nil), nil, nil, nil, nil, nil, nil, nil, nil, nil, finding)
	const internalToken, externalToken = "internal-token", "external-token"
	ts := httptest.NewServer(MCPHttpServerCreate("0", internalToken, []string{externalToken}, nil,
		MCPServerCreate(handler.Register), MCPExternalServerCreate(handler.RegisterExternal)).Handler)
	defer ts.Close()

	connect := func(token string) *mcp.ClientSession {
		client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
			Endpoint:   ts.URL,
			HTTPClient: &http.Client{Transport: &bearerTransport{token: token}},
		}, nil)
		require.NoError(t, err)
		return session
	}
	toolNames := func(session *mcp.ClientSession) []string {
		tools, err := session.ListTools(ctx, nil)
		require.NoError(t, err)
		names := make([]string, 0, len(tools.Tools))
		for _, tool := range tools.Tools {
			names = append(names, tool.Name)
			if tool.Name == "report_security_finding" {
				assert.False(t, tool.Annotations.ReadOnlyHint)
			}
		}
		return names
	}
	args := map[string]any{"ip": "198.51.100.7", "severity": "critical", "title": "t", "text": "x"}

	agent := connect(internalToken)
	defer func() { _ = agent.Close() }()
	assert.NotContains(t, toolNames(agent), "report_security_finding")
	res, err := agent.CallTool(ctx, &mcp.CallToolParams{Name: "report_security_finding", Arguments: args})
	if err == nil {
		assert.True(t, res.IsError)
	}
	assert.Equal(t, 0, finding.calls)
	assert.Contains(t, agent.InitializeResult().Instructions, "Все инструменты read-only")
	assert.NotContains(t, agent.InitializeResult().Instructions, "report_security_finding")

	external := connect(externalToken)
	defer func() { _ = external.Close() }()
	externalNames := toolNames(external)
	assert.Contains(t, externalNames, "report_security_finding")
	assert.LessOrEqual(t, len(externalNames), 16, "не более 16 инструментов у внешних клиентов")
	assert.Contains(t, external.InitializeResult().Instructions, "report_security_finding")
	res, err = external.CallTool(ctx, &mcp.CallToolParams{Name: "report_security_finding", Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError)
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	assert.JSONEq(t, `{"key":"security:198.51.100.7","id":5,"deduplicated":false}`, string(raw))
	assert.Equal(t, 1, finding.calls)
}
