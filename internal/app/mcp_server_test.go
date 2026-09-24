package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mechta-market/pulse/internal/constant"
	handlerMcpP "github.com/mechta-market/pulse/internal/handler/mcp"
	usecaseSystemP "github.com/mechta-market/pulse/internal/usecase/system"
)

// pingClient подключается к тестовому серверу и вызывает ping — критерий приёмки фазы 0.
func TestMCPServer_Ping(t *testing.T) {
	ctx := context.Background()

	systemUsecase := usecaseSystemP.New([]usecaseSystemP.Source{
		{Name: "ok_source", Ping: func(context.Context) error { return nil }},
		{Name: "broken_source", Ping: func(context.Context) error { return errors.New("connection refused") }},
		{Name: "disabled_source"},
	})
	handler := handlerMcpP.New(systemUsecase, nil, nil, nil, nil, nil, nil, nil, nil)
	server := MCPServerCreate(handler.Register)

	// токен бота и внешних клиентов; пустые игнорируются
	const token, externalToken = "secret-token", "external-token"
	httpServer := MCPHttpServerCreate("0", "/mcp", []string{token, externalToken, ""}, server)
	ts := httptest.NewServer(httpServer.Handler)
	defer ts.Close()

	t.Run("unauthorized without token", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/mcp", "application/json", nil)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	for name, bearer := range map[string]string{"wrong token": "nope", "empty token": ""} {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", nil)
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
			Endpoint:   ts.URL + "/mcp",
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
			Endpoint:   ts.URL + "/mcp",
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
		assert.LessOrEqual(t, len(names), 13, "не более 13 инструментов (ТЗ, раздел 8)")

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
