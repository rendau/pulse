# pulse

MCP-сервер инфраструктурного контекста: единая точка, через которую LLM-агент получает
информацию о сервисах компании (каталог, состояние в Kubernetes, метрики, логи, изменения).
ТЗ — `docs/infra-mcp-spec.md`.

## Запуск

```
cp .env.example .env         # подключения, порты, токены
cp conf.example.yml conf.yml # правила маппинга образов, исключения namespace
go run ./cmd/
```

Адреса и токены источников — только через env (см. `.env.example`): `PROMETHEUS_URL`/`_TOKEN`/`_ORG_ID`,
`LOKI_URL`/`_TOKEN`/`_ORG_ID`, `ALERTMANAGER_URL`/`_TOKEN`, `KUSEC_URL`/`_TOKEN`, `RUTO_URL`, `GITHUB_TOKEN`, `REGISTRY_TOKEN`.
`GITHUB_TOKEN` — на чтение репозиториев, пакетов (`read:packages`) и запусков Actions: по ним
индексер находит коммит запущенного образа без OCI-label'ов.
Basic-auth задаётся userinfo в URL (`https://user:pass@host`). Пустой URL — источник выключен.

MCP-эндпоинт: `http://localhost:${HTTP_PORT}${MCP_PATH}` (streamable HTTP, stateless),
авторизация — `Authorization: Bearer <токен>`: `MCP_AUTH_TOKEN` (бот pulse_bot) или любой из
`MCP_EXTERNAL_TOKENS` (внешние клиенты, через запятую).
Служебные ручки на `SYSTEM_HTTP_PORT`: `/healthcheck`, `/readiness`, `/metrics`, `/docs/*`.

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
(ветка по умолчанию). Схема — раздел 1.2 ТЗ. Отсутствие файла — не ошибка: сервис попадает
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
