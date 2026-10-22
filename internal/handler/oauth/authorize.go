package oauth

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/rendau/pulse/internal/errs"
	usecaseOAuth "github.com/rendau/pulse/internal/usecase/oauth"
	"github.com/rendau/pulse/internal/util/clientip"
)

// wrongKeyDelay замедляет перебор ключей доступа на странице согласия.
const wrongKeyDelay = time.Second

// authorizeFields — параметры запроса авторизации; страница согласия несёт их скрытыми полями.
var authorizeFields = []string{
	"response_type", "client_id", "redirect_uri", "state",
	"code_challenge", "code_challenge_method", "scope", "resource",
}

type authorizePage struct {
	ClientName   string
	RedirectHost string
	Fields       map[string]string
	Error        string
}

var authorizeTemplate = template.Must(template.New("authorize").Parse(`<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>pulse — подключение</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;background:#f4f5f7;color:#1f2328;margin:0;padding:16px}
main{max-width:420px;margin:48px auto;background:#fff;border-radius:12px;padding:24px;box-shadow:0 1px 3px rgba(0,0,0,.12)}
h1{font-size:20px;margin:0 0 16px}
p{line-height:1.5;margin:0 0 12px}
.host{color:#57606a;font-size:14px}
label{display:block;font-size:14px;margin:16px 0 6px}
input[type=password]{width:100%;box-sizing:border-box;padding:10px;font-size:16px;border:1px solid #d0d7de;border-radius:8px}
.error{background:#ffebe9;color:#a40e26;border-radius:8px;padding:10px;margin:12px 0 0;font-size:14px}
.actions{display:flex;gap:8px;margin-top:20px}
button{flex:1;padding:10px;font-size:15px;border-radius:8px;border:1px solid #d0d7de;background:#f6f8fa;cursor:pointer}
button.allow{background:#1f883d;border-color:#1f883d;color:#fff}
</style>
</head>
<body>
<main>
{{if .Fields}}
<h1>Подключение к pulse</h1>
<p><b>{{.ClientName}}</b> просит доступ к MCP-серверу pulse: каталог сервисов, их состояние, метрики, логи и изменения (только чтение).</p>
<p class="host">Ответ уйдёт на {{.RedirectHost}}</p>
<form method="post">
{{range $name, $value := .Fields}}<input type="hidden" name="{{$name}}" value="{{$value}}">
{{end}}<label for="key">Ключ доступа</label>
<input id="key" name="key" type="password" autocomplete="off" autofocus required>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<div class="actions">
<button type="submit" name="action" value="deny" formnovalidate>Отклонить</button>
<button type="submit" name="action" value="allow" class="allow">Разрешить</button>
</div>
</form>
{{else}}
<h1>Подключение невозможно</h1>
<p class="error">{{.Error}}</p>
<p>Подключите сервер в клиенте заново.</p>
{{end}}
</main>
</body>
</html>
`))

// handleAuthorize — страница согласия: GET показывает форму, POST записывает решение и
// возвращает браузер клиенту. Пользователей у pulse нет: разрешает тот, кто знает ключ доступа.
func (h *Handler) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	var values url.Values
	switch r.Method {
	case http.MethodGet:
		values = r.URL.Query()
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, formMaxBytes)
		if err := r.ParseForm(); err != nil {
			renderAuthorize(w, http.StatusBadRequest, &authorizePage{Error: "Неверный запрос."})
			return
		}
		values = r.PostForm
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	req := &usecaseOAuth.AuthorizeReq{
		ResponseType:        values.Get("response_type"),
		ClientId:            values.Get("client_id"),
		RedirectUri:         values.Get("redirect_uri"),
		State:               values.Get("state"),
		CodeChallenge:       values.Get("code_challenge"),
		CodeChallengeMethod: values.Get("code_challenge_method"),
		Scope:               values.Get("scope"),
		Resource:            values.Get("resource"),
	}

	info, err := h.uc.DescribeAuthorization(r.Context(), req)
	if err != nil {
		renderAuthorizeError(w, r, err)
		return
	}

	page := &authorizePage{
		ClientName:   info.ClientName,
		RedirectHost: info.RedirectHost,
		Fields:       make(map[string]string, len(authorizeFields)),
	}
	for _, name := range authorizeFields {
		if v := values.Get(name); v != "" {
			page.Fields[name] = v
		}
	}

	if r.Method == http.MethodGet {
		renderAuthorize(w, http.StatusOK, page)
		return
	}

	redirectUrl, err := h.uc.Authorize(r.Context(), req, values.Get("key"), values.Get("action") == "allow")
	if err != nil {
		if errors.Is(err, errs.OAuthInvalidKey) {
			slog.Warn("mcp oauth: wrong access key on the consent page",
				"client_name", info.ClientName, "client_ip", clientip.FromRequest(r))
			select {
			case <-r.Context().Done():
				return
			case <-time.After(wrongKeyDelay):
			}
			page.Error = "Ключ не подходит."
			renderAuthorize(w, http.StatusForbidden, page)
			return
		}
		renderAuthorizeError(w, r, err)
		return
	}

	http.Redirect(w, r, redirectUrl, http.StatusSeeOther)
}

func renderAuthorizeError(w http.ResponseWriter, r *http.Request, err error) {
	if full, ok := errs.AsErrFull(err); ok {
		renderAuthorize(w, http.StatusBadRequest, &authorizePage{Error: full.Desc})
		return
	}
	if errors.Is(err, errs.OAuthNotEnabled) {
		renderAuthorize(w, http.StatusNotFound, &authorizePage{Error: "OAuth на этом сервере выключен."})
		return
	}
	slog.Error("mcp oauth: authorize", "client_ip", clientip.FromRequest(r), "error", err)
	renderAuthorize(w, http.StatusInternalServerError, &authorizePage{Error: "Внутренняя ошибка, попробуйте ещё раз."})
}

func renderAuthorize(w http.ResponseWriter, status int, page *authorizePage) {
	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Frame-Options", "DENY")
	// form-action не задаётся: после отправки формы браузер уходит редиректом на адрес клиента
	header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	header.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	if err := authorizeTemplate.Execute(w, page); err != nil {
		slog.Warn("mcp oauth: render authorize page", "error", err)
	}
}
