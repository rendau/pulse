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

MCP-эндпоинт: `http://localhost:${HTTP_PORT}${MCP_PATH}` (streamable HTTP, stateless),
авторизация — `Authorization: Bearer ${MCP_AUTH_TOKEN}`.
Служебные ручки на `SYSTEM_HTTP_PORT`: `/healthcheck`, `/readiness`, `/metrics`, `/docs/*`.

## Инструменты

| Инструмент | Назначение |
|---|---|
| `ping` | версия и статус подключения к источникам |
| `resolve_service` | человеческая формулировка → кандидаты каталога |
| `list_services` | обзорный список с фильтрами (team, namespace, criticality, has_metadata) |
| `get_service_info` | карточка сервиса: метаданные, workloads, живое состояние подов |

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
