# pulse

MCP-сервер инфраструктурного контекста: единая точка, через которую LLM-агент получает
информацию о сервисах компании (каталог, состояние в Kubernetes, метрики, логи, изменения).

## Запуск

```
cp .env.example .env         # подключения, порты, токены
cp conf.example.yml conf.yml # правила маппинга образов, исключения namespace
go run ./cmd/
```

Адреса и токены источников — только через env (см. `.env.example`): `PROMETHEUS_URL`/`_TOKEN`/`_ORG_ID`,
`LOKI_URL`/`_TOKEN`/`_ORG_ID`, `ALERTMANAGER_URL`/`_TOKEN`, `KUSEC_URL`/`_TOKEN`, `RUTO_URL` (служебный порт ruto-core: `http://ruto-core.<namespace>.svc:3003`), `GITHUB_TOKEN`, `REGISTRY_TOKEN`.
`GITHUB_TOKEN` — на чтение репозиториев, пакетов (`read:packages`) и запусков Actions: по ним
индексер находит коммит запущенного образа без OCI-label'ов.
Basic-auth задаётся userinfo в URL (`https://user:pass@host`). Пустой URL — источник выключен.

MCP-эндпоинт: `http://localhost:${MCP_PORT}/` (streamable HTTP, stateless; `MCP_PORT` по умолчанию
`5060`). Порт отдельный, поэтому свой путь MCP не нужен: транспорт отвечает на любом пути, кроме адресов
OAuth. На MCP-порту больше ничего нет: наружу публикуется только он. Авторизация — `Authorization: Bearer <токен>`: `MCP_AUTH_TOKEN` (агент
pulse_agent) или любой из `MCP_EXTERNAL_TOKENS` (внешние клиенты, через запятую), а с OAuth — ещё
access-токен OAuth.
Служебные ручки на `SYSTEM_HTTP_PORT`: `/healthcheck`, `/readiness`, `/metrics`.

## Вход через OAuth (коннекторы Claude)

Коннекторы claude.ai / Claude Desktop / мобильного Claude не умеют свой заголовок `Authorization` —
им нужен OAuth. Человек вписывает в клиент только адрес MCP-сервера, клиент открывает страницу
согласия pulse, человек вводит **ключ доступа** (один из `MCP_EXTERNAL_TOKENS`) и нажимает
«Разрешить». Пользователей у pulse нет: разрешает тот, кто знает ключ. Статические токены работают
рядом.

Включение: `MCP_OAUTH_ENABLED=true` + `MCP_PUBLIC_URL` (адрес MCP-порта как его видят клиенты,
напр. `https://pulse-mcp.example.com`) + `MCP_OAUTH_SECRET` (ключ подписи `client_id`,
≥32 случайных символов; смена — клиенты подключаются заново) + непустой `MCP_EXTERNAL_TOKENS`.
Без этого pulse не стартует.

- **Подключение = ключ.** Подключение (таблица `oauth_grant`) помнит sha256 ключа, которым его
  разрешили; на каждом запросе ключ сверяется с текущим `MCP_EXTERNAL_TOKENS` — убрали ключ из env,
  его подключения перестают работать. `MCP_AUTH_TOKEN` агента подключение не разрешает, и клиенты
  OAuth не получают ответов ручек для человека (`audience: human`).
- **Токены.** Access `pso_…` живёт `MCP_OAUTH_ACCESS_TTL` (1h), refresh `psr_…` — `MCP_OAUTH_REFRESH_TTL`
  (720h), при обновлении выдаётся новая пара, старый refresh не принимается. Код `psc_…` одноразовый,
  2 минуты, PKCE S256 обязателен. В БД (`oauth_token`) — только sha256. Отзыв (`/revoke`) удаляет
  подключение целиком; истёкшее чистится раз в час.
- **Регистрация клиента** (`POST <MCP_PUBLIC_URL>/register`, RFC 7591) открыта по протоколу; код уходит
  только на адреса из `MCP_OAUTH_REDIRECT_URIS` (по умолчанию claude.ai, claude.com и loopback —
  `localhost`/`127.0.0.1` с любым портом для Claude Code). В БД регистрация не пишет: `client_id`
  (`pscl_…`) — подписанные данные клиента.
- **Адреса на MCP-порту:** `/.well-known/oauth-protected-resource`,
  `/.well-known/oauth-authorization-server` (и `openid-configuration`), `/authorize` (страница
  согласия), `/register`, `/token`, `/revoke`. Если порт опубликован под путём
  (`MCP_PUBLIC_URL=https://host/pulse`), адреса живут под этим путём, метаданные — ещё и в форме RFC
  (`/.well-known/…/pulse`) и в корне.
  401 от MCP несёт `WWW-Authenticate: Bearer resource_metadata=…` — с него клиент начинает вход.
- **Сеть.** Коннекторы claude.ai ходят в MCP-порт с серверов Anthropic — порт должен быть доступен из
  интернета, Cloudflare перед ним не должен их блокировать. Лучше отдельный поддомен; если под путём —
  ingress не должен его срезать.
- **Журнал.** Каждый запрос MCP-порта — строка `mcp: request` (метод, путь, JSON-RPC метод, статус,
  время, вид учётных данных `none`/`static`/`oauth` — без значений, адрес клиента через
  `Cf-Connecting-Ip`/`X-Forwarded-For`/`X-Real-Ip`, `cf_ray`, User-Agent). Отказы — `mcp: credential
  rejected` и `oauth: access token rejected` (неизвестный, истёкший, подключение удалено, ключ убран из
  env), `mcp oauth: request rejected`, `mcp oauth: wrong access key on the consent page`; шаги — `mcp
  oauth: client registered`, `oauth: consent`, `oauth: tokens issued`, `oauth: connection revoked by the client`.

Подключение: claude.ai / Claude Desktop — Settings → Connectors → Add custom connector → адрес
`MCP_PUBLIC_URL`. Claude Code — `.mcp.json` без заголовка, вход — `/mcp` → Authenticate:

```json
{"mcpServers": {"pulse": {"type": "http", "url": "https://pulse-mcp.example.com/"}}}
```

С токеном (агенты, скрипты) — как раньше: `"headers": {"Authorization": "Bearer ${PULSE_MCP_TOKEN}"}`.

**Деплой.** MCP переехал с `HTTP_PORT` на `MCP_PORT`: в чарте `helm-zeon/charts/pulse` порт контейнера и
Service (порт `http`) — на `MCP_PORT`, `PULSE_MCP_URL` у pulse_agent — на этот порт (путь не важен:
`http://pulse.default:5060/` или прежний `…/mcp`); для коннекторов —
ingress на MCP-порт и env OAuth выше.

## Подключить сервис

Стандарт — [docs/service-manifest.md](docs/service-manifest.md). Агенту (Claude Code и т. п.) в
репозитории сервиса достаточно одной фразы — остальное в [docs/connect-service.md](docs/connect-service.md):

```
Подключи сервис к pulse по инструкции https://raw.githubusercontent.com/rendau/pulse/master/docs/connect-service.md
```

## Инструменты

| Инструмент | Назначение |
|---|---|
| `ping` | версия и статус подключения к источникам |
| `resolve_service` | человеческая формулировка → кандидаты каталога |
| `list_services` | обзорный список с фильтрами (team, namespace, criticality, has_metadata) |
| `get_service_info` | карточка сервиса: метаданные, workloads, живое состояние подов |
| `get_service_snapshot` | срез состояния: алерты, поды, метрики с базовой линией, события, health |
| `query_metrics` | временной ряд по `metric_id` сервиса или произвольному PromQL |
| `query_logs` | логи из Loki: агрегированные паттерны со счётчиком или последние строки |
| `get_timeline` | деплои, коммиты, смена конфигурации (reloader), алерты, рестарты на одной оси времени |
| `get_changes` | коммиты, что не в проде, история деплоев, диффы конфигурации (секреты маскированы) |
| `get_dependencies` | граф сконфигурированных связей (env/configmap, маршруты ruto) с кратким здоровьем соседей |
| `get_public_api` | внешний контур через gateway ruto: домен, опубликованные маршруты, rps/5xx/p95 и коды ответов |
| `call_service_endpoint` | вызов диагностической ручки из `service.yaml` (только объявленный id, только GET, PII маскированы) |
| `get_cluster_health` | ноды, проблемные и pending-поды, Warning-события, инфра-алерты, загрузка кластера |

Диагностические ручки (`endpoints` в `service.yaml`) вызываются на ClusterIP сервиса по DNS
`<k8s_service>.<namespace>.svc:<port>`; для локальной разработки `ENDPOINT_CALL_MODE=k8s-proxy`
(через API-сервер, нужен RBAC `services/proxy`).

## service.yaml

Метаданные сервиса читаются индексером из файла `service.yaml` в корне репозитория
(ветка по умолчанию). Схема — `internal/service/indexer/service/model/service_yaml.go`. Отсутствие файла — не ошибка: сервис попадает
в каталог с данными из кластера и `has_metadata: false`.

## Сборка и проверка

```
make build     # cmd/build/svc
make test
make lint
```

### DB dump / restore

```
pg_dump --no-owner -Fc -U postgres pulse -f ./pulse.custom
dropdb -U postgres pulse && createdb -U postgres pulse
pg_restore --no-owner -d pulse -U postgres ./pulse.custom
```

### Миграции

Инструмент: https://github.com/golang-migrate/migrate/tree/master/cmd/migrate

```
migrate create -ext sql -dir migrations mg_name
migrate -path migrations -database "postgres://localhost:5432/db_name?sslmode=disable" up
```
