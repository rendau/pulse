# CLAUDE.md

Руководство для Claude Code по работе с этим репозиторием. Go-сервис на Clean Architecture + DDD:
MCP-сервер (streamable HTTP, `github.com/modelcontextprotocol/go-sdk`), Postgres (pgx/mobone),
клиенты Kubernetes/GitHub/registry/Prometheus/Loki/Alertmanager. ТЗ — `docs/infra-mcp-spec.md`
(читать целиком перед работой над любой фазой; в ТЗ сервис назван `infra-mcp`).

Согласованные с заказчиком отклонения от ТЗ и шаблона:
- gRPC/grpc-gateway/proto из шаблона убраны — транспорт только MCP поверх HTTP.
- Теги образов в кластере — `latest` (keel), поэтому `deployed_commit` берётся не из тега,
  а из digest запущенного пода → OCI-label `org.opencontainers.image.revision` (registry API),
  а если label'а нет (сервисы собираются без них, правки CI сервисов не делаем) — digest → версия
  пакета ghcr (время публикации) → push-запуск GitHub Actions в это время → `head_sha`.
  Если тег похож на SHA — используется тег.
- jsonb-колонка `service.metadata` хранится байтовым способом (`[]byte` + repo-локальная DTO).

Конвенции этого стека вынесены в глобальные Claude Code скиллы (`crud`, `mobone`,
`golang-service`, `golang-samber-lo`) — они подхватываются автоматически по описанию.

---

## Структура проекта

### Верхний уровень
- `cmd/main.go` — entrypoint, поднимает `internal/app.App`.
- `internal/` — бизнес-логика и инфраструктура (закрытые пакеты).
- `migrations/` — SQL миграции Postgres (единый `000001_init` до первого деплоя).
- `docs/` — ТЗ и статические доки, выдаются через `/docs/*`.
- `conf.example.yml` — пример yaml-правил (`RULES_PATH`, по умолчанию `./conf.yml`).
- `Dockerfile`, `Makefile` — сборка (`make build` подставляет версию через ldflags).
- `.env.example` — пример окружения.

### Внутренние пакеты (`internal/`)
- `internal/app/` — сборка приложения: серверы, миграции, метрики, трассировка, HTTP-gateway, DI.
  - `app.go` — граф зависимостей и запуск компонентов.
  - `mcp_server.go` — MCP-сервер, HTTP-транспорт на `MCP_PATH`, bearer-auth middleware.
  - `system_http_server.go` — системный HTTP-сервер (`SYSTEM_HTTP_PORT`, дефолт 3003):
    /healthcheck, /readiness, /docs/*, /metrics.
  - `migration.go` — запуск миграций из `migrations/`.
- `internal/config/` — `config.go` (env: адреса, токены, порты — **все адреса и токены источников
  только через env**) и `rules.go` (yaml-правила: `image_mapping`, `indexer.*`, `snapshot.*`,
  `metrics.*`).
- `internal/handler/` — транспортный слой.
  - `mcp/` — MCP-инструменты (`handler.go` — регистрация и описания, по файлу на группу).
  - `mcp/dto/` — преобразование usecase-моделей ↔ JSON-ответы инструментов (теги только тут).
- `internal/infra/httpx/` — единая фабрика http-клиентов (таймауты, лимиты; все клиенты только через неё).
- `internal/util/` — `imageref` (разбор ссылок на образы), `fuzzy` (нечёткое сравнение),
  `window` (разбор окна), `tz` (часовой пояс ответов — Asia/Almaty, база поясов вшита в бинарник; все времена в DTO и в текстах summary/details идут через `tz.In`), `redact` (маскирование конфигурации/секретов/PII, ТЗ 4.2 — с тестами).
- `internal/usecase/` — usecase-слой (валидация, оркестрация сервисов и доменных сервисов):
  `system` (ping), `catalog` (resolve/list/info), `snapshot` (fan-out снапшота, query_metrics),
  `logs` (query_logs, top_errors), `timeline` (get_timeline, get_changes), `dependencies`
  (get_dependencies: обход графа в ширину с лимитом узлов, здоровье соседей по подам),
  `endpoints` (call_service_endpoint: allowlist по id, только GET, валидация параметров,
  PII через redact, лимиты строк/байт/таймаута), `cluster` (get_cluster_health: ноды, поды
  по кластеру (под без workload'а каталога — к сервису по репозиторию образа или
  `app.kubernetes.io/managed-by`), Warning-события по причинам с сервисами их объектов, инфра-алерты = не привязанные к каталогу,
  метрики кластера с базовой линией), `publicapi` (get_public_api: приложения ruto сервиса по рёбрам
  индексера, маршруты из снапшота ruto, трафик из метрик gateway). Исключение из правила
  «usecase не ходит в соседний usecase»:
  `snapshot` берёт `top_errors` у `logs` через узкий порт `LogsI`, чтобы не дублировать
  селектор + выборку + агрегацию.
- `internal/domain/` — доменная модель, сервисы и репозитории.
  - `svc`, `workload` — каталог (Postgres); `snapshot` — детерминированные правила снапшота
    (health, summary_hints, базовая линия) без обращения к источникам; `event` — нормализованное
    событие (ТЗ 1.2) и его нормализация из фактов кластера/деплоев (`event/service`);
    `logs` — нормализация строк и агрегация в паттерны (чистые функции); `deploy` — история
    деплоев (Postgres), пишется индексером при смене образа/digest; `dependency` — сконфигурированные
    связи «сервис → хост» (Postgres) и разбор адресов из значений конфигурации (`ParseEndpoints`,
    `ClusterHost`); `cluster` — правила здоровья кластера и подсказки (фаза 7.1).
  - `*/model/` — доменные структуры (entity).
  - `*/service/` — доменные сервисы (инварианты/логика).
  - `*/repo/` — репозитории.
  - `common/` — общие модели/утилиты/PG базовый репозиторий.
- `internal/service/` — сервисы (фоновые/инфраструктурные), для переиспользования или выделения логики:
  `k8s`, `github`, `registry`, `prometheus`, `loki`, `alertmanager`, `kusec` (клиенты источников,
  read-only; kusec — по контракту `docs/monitoring-api.md` проекта kusec, ключ scope=read_only),
  `ruto` (снапшот конфигурации gateway ruto-core, кэш по версии; секретные поля не разбираются),
  `svcproxy` (GET к ручке сервиса внутри кластера по DNS `name.namespace.svc`; локально —
  `ENDPOINT_CALL_MODE=k8s-proxy` через `k8s.ProxyGet`),
  `indexer` (фоновый обход кластера → каталог + история деплоев).
- `internal/errs/` и `internal/constant/` — общие коды ошибок и константы.

---

## Архитектура: слои и зависимости

- **Transport** (`internal/handler/mcp/*`):
  - Работает только с DTO инструментов и usecase-интерфейсами.
  - Не обращается напрямую к репозиториям и сервисам.
  - Описание инструмента — часть продукта: когда выбирать / когда нет, что возвращает, 4–5 строк.
  - Общее число инструментов — не более 12–13 (см. раздел 8 ТЗ); новые — объединять с существующими.
  `list_service_endpoints` из ТЗ влит в `get_service_info` (`diagnostic_endpoints`). Сейчас 13
  инструментов — лимит исчерпан, новые только объединением с существующими.
- RBAC индексера и инструментов: get/list на nodes, deployments, statefulsets, daemonsets, cronjobs,
  replicasets, pods, events, configmaps, services; для `ENDPOINT_CALL_MODE=k8s-proxy` — get на services/proxy.
- **Usecase** (`internal/usecase/*`):
  - Входной слой от транспортного слоя (запросы от внешних систем).
  - Валидация входных параметров.
  - Оркестрация доменных сервисов и сервисов `internal/service/*`.
  - Вход/выход — доменные модели (не protobuf).
  - Желательно не обращается в соседние usecases.
- **Domain** (`internal/domain/*`):
  - `model/` — структуры данных, сущности (entity).
    - **Запрещены теги сериализации** (`json:"..."`, `yaml:"..."` и т.п.) на полях доменной
      модели — она не знает про транспорт и про формат хранения.
  - `service/` — доменные операции и инварианты. Может использовать только репозиторий.
  - `repo/` — доступ к хранилищам. Может содержать подпапки для разных типов хранилищ или моков.
- **Service** (Infrastructure/background/external integrations, `internal/service/*`):
  - Фоновые процессы, интеграции с внешними системами.
  - Выделенные или переиспользуемые логики.
  - Может использовать другие сервисы `internal/service/*` и доменные сервисы `internal/domain/*/service`.
  - Не обращается в usecase слой.
- **Composition** (`internal/app/`):
  - Сборка зависимостей, запуск серверов, миграций, фоновых сервисов.

### Правило зависимостей
```
handler        → usecase
usecase        → domain service
usecase        → service
service        → service
service        → domain service
domain service → repo
```
- Обратные зависимости **запрещены**.
- К `repo` слою доступ только из `domain service`.

### Источники и авторизация
- Опциональные источники (`PROMETHEUS_URL`, `LOKI_URL`, `ALERTMANAGER_URL`, `RUTO_URL`) — nil-указатель при пустом URL;
  в usecase передаётся nil-интерфейс (не nil-указатель в интерфейсе), ответ содержит `errors` с
  `not configured`.
- Авторизация клиента: `*_TOKEN` (bearer), userinfo в URL (basic), `*_ORG_ID` (X-Scope-OrgID) —
  всё в `Auth` конструктора `New(baseUrl, Auth)`; заголовки ставит только `sendRequest`.
- Параметр `window` разбирается `internal/util/window` (дефолт 1h, максимум 7d, понимает `7d`);
  у логов свой потолок `logs.max_window` (24h).
- Логи: селектор из `service.yaml` (`logs.selector`), иначе `logs.default_selector` из правил
  с плейсхолдерами `{namespace}`, `{pod_regex}`; запрос без привязки к сервису невозможен.
  В проде логи шлёт fluent-bit: лейблы `kubernetes_namespace_name`, `kubernetes_pod_name`.
- Метрики приложений (go-шаблон) в проде с префиксом `<ns>_<svc>_request_total` (у старых —
  `_request_count`) и `status=ok|error`: дефолтные golden signals ищут имя регэкспом `__name__`.
  Сервисам, опубликованным в ruto, снапшот добавляет `public_*` по метрикам gateway (`{ruto_apps}`).

### Хранилища
- Postgres: только топология и метаданные (`service`, `workload`). Значений метрик, логов и
  счётчиков в БД быть не должно (Р4 ТЗ) — всё состояние запрашивается живьём.
- Домен сущности «сервис каталога» лежит в `internal/domain/svc` (таблица `service`).
- **Имена таблиц всегда в единственном числе**, без plural: `usr`, `app`, `secret`, `item`
  (не `usrs`, `apps`, `secrets`, `items`). То же значение указывается в `TableName` репозитория.

### Миграции
- Файлы в `migrations/` в формате `NNNNNN_<name>.up.sql` / `.down.sql` (golang-migrate).
- В `down`-миграциях во всех командах `DROP` обязательно указывать `CASCADE`
  (напр. `drop table if exists <table> cascade;`).
- В `down` объекты удаляются в порядке, обратном `up` (с учётом внешних ключей).

### API (MCP)
- Инструменты регистрируются в `internal/handler/mcp/handler.go` через `mcp.AddTool` с типизированными
  In/Out DTO (`internal/handler/mcp/dto`); схема выводится из json/jsonschema-тегов.
- Все инструменты read-only (Р5 ТЗ): `ToolAnnotations{ReadOnlyHint: true}`.
- Ошибка источника не роняет ответ: частичный результат + поле `errors: [{source, message}]`.
- Семантические ошибки (неизвестный сервис, неверный параметр) отдаются моделью как текст
  ошибки инструмента с подсказкой (см. `handler/mcp/errors.go`); неизвестное имя сервиса —
  всегда со списком похожих имён.
- **Пагинация: `page` начинается с `0`.**
- Ответ инструмента ≤ 100 KB; при усечении — `truncated: true` и `total_count`.

### Ошибки и валидация
- Семантические ошибки — через `internal/errs` (см. gRPC interceptor в `internal/app/grpc.go`).
- Валидация параметров — в usecase.
- Нельзя пробрасывать ошибки наружу без wrapping (оборачивать в `fmt.Errorf("...: %w")`).
- Для работы с ошибками всегда используй `errors.Is` и `errors.AsType`. Избегай прямого
  сравнения ошибок (`==`) и type assertion (`err.(*MyError)`), чтобы корректно обрабатывать
  обёрнутые ошибки.

### Правила изменения кода
- DTO инструментов не должны протекать в usecase и доменные сервисы.
- Секреты не покидают сервис (Р7 ТЗ): значения секретов никогда не попадают в ответ. Любое значение
  конфигурации перед выдачей проходит `redact.Value(key, value)` (deny-список имени → allowlist
  значения → маска), значения secret — только `redact.Secret()`.
- История алертов: Alertmanager её не хранит, берётся из Prometheus (`ALERTS{alertstate="firing"}`).
  Привязка алерта к сервису и показ лейблов — только через `snapshot/service/alerts.go`
  (`AlertOwner`/`AlertLabels`): лейблы экспортеров (`job=kube-state-metrics` и т.п.) — не объект
  алерта; Watchdog не показывается; повторы одного алерта сливаются в один со счётчиком.
- Смена конфигурации: reloader перекатывает поды после смены configmap/secret и пишет
  в шаблон пода аннотацию `reloader.stakater.com/last-reloaded-from` (объект + хэш). Таймлайн и
  get_changes сравнивают соседние ревизии ReplicaSet (`source: reloader`); отпечаток secret скрыт.
- kusec: приложение сервиса находится по `workload.config_refs` (имена configmap/secret шаблона пода)
  → `/app/resolve`. Правки — из `/audit` (`source: kusec`; значение секрета — `***`, обычный конфиг —
  через `redact.Value`), применение — из `/sync-run` (объекты только через Get). Sync, изменивший
  объект выкатки reloader'а за ≤15 мин до неё, склеивается с выкаткой (автор + ключи), остальные —
  `kusec_sync`. `/app/{id}/drift` → `unsynced_config` в get_changes. Аннотацию `kusec.io/sync-run-id`
  с Secret не читаем: это потребовало бы RBAC на secrets.
- Маршруты ruto индексер пишет в граф рёбрами `ruto-gateway → backend` (`source=ruto`, `key` —
  имя приложения ruto): по ним находятся приложения сервиса для get_public_api и `public_*` метрик.
- Имя сервиса выводится из пути образа: имя репозитория из первых двух сегментов (`{repo}`).
  ghcr.io кладёт образы внутрь репозитория, поэтому `ghcr.io/rendau/loom/server` и
  `…/loom/artifact` — один сервис `loom` с двумя workload'ами. Репозиторий (`repo_url`) для
  ghcr берётся из привязки пакета в GitHub (`PackageRepoUrl`: `dpm` → `dp-mechta`,
  `ruto-core` → `ruto`), иначе — по шаблону `image_mapping`; имя сервиса от этого не меняется.
  Образ без правила маппинга (postgres, redis) — сторонний, сервис называется по workload'у.
- Граф зависимостей (фаза 5) строится индексером из env подов: inline-значения и ссылки на configmap
  (в helm-zeon env рендерится inline). Переменные из secret дают только факт ссылки, значение не
  читается. Хост → сервис: k8s Service (селектор) → workload, иначе имя workload'а в namespace.
  kusec графу ничего не добавляет: значения секретов (DSN) он не отдаёт, а configmap индексер
  читает сам. RBAC индексера:
  get/list на deployments, statefulsets, daemonsets, cronjobs, pods, events, configmaps, services.
- В тестах всегда предпочитай `testify`: `require` для проверок, прерывающих тест,
  и `assert` для остальных утверждений.
- При реализации worker pool / параллельной обработки используй `errgroup`
  (golang.org/x/sync/errgroup), а не ручное управление горутинами через `sync.WaitGroup` + каналы.

---

## Композиционный корень (`internal/app/app.go`)

`app.go` — единственная точка композиции приложения: здесь собирается граф зависимостей
(`repo → service → usecase → handler`) и описывается жизненный цикл. Бизнес-логики тут нет —
только связывание компонентов и управление их запуском/остановкой.

### Тип `App`
- В поля выносится **только то, чем нужно управлять после `Init`**: то, что надо явно
  останавливать (серверы), ждать (фоновые сервисы/обработчики), закрывать (трассировщик,
  pgx pool), а также корневой `ctx` с его `ctxCancel` и `exitCode`.
- Локальные звенья графа (repo, service, usecase, handler), которые нужны только для сборки и
  сразу передаются дальше, **не** выносятся в поля — это локальные переменные внутри `Init`.

### Импорты Композиционного корня
- Группируются блоками с пустой строкой между группами: стандартная библиотека → внешние
  зависимости → внутренние пакеты проекта.
- Внутренние пакеты-конструкторы импортируются с суффиксом-алиасом `P`, и в алиасе прописывается весь путь в camel-case
  (например, `internal/service/mdm` -> `serviceMdmP`, `internal/handler/grpc` -> `handlerGrpcP`, `internal/service/2gis/service` -> `service2gisServiceP`).

### Методы-фазы жизненного цикла
Фиксированный набор методов, каждый делает ровно одно:
- `Init` — создание и связывание всех зависимостей.
- `PreStartHook` — действия перед стартом.
- `Start` — запуск серверов и фоновых сервисов.
- `Listen` — блокировка до сигнала ОС (`SIGINT`/`SIGTERM`).
- `Stop` — отмена контекста и graceful-остановка серверов.
- `WaitJobs` — ожидание завершения фоновых задач.
- `Exit` — закрытие ресурсов и выход с `exitCode`.

### Стиль `Init`
- Сборка идёт **сверху вниз в порядке зависимостей**: инфраструктура (логгер, трассировка,
  pgx pool, кэш, миграции) → доменные блоки → сервисы → серверы.
- Каждый логический блок предваряется коротким комментарием-меткой в нижнем регистре
  (`// ord`, `// checkout`, `// grpc server`).
- Внутри блока цепочка строится единообразно:
  `repo := ...New(...)` → `service := ...New(repo)` → `usecase := ...New(service)` →
  `handler := ...New(usecase)`.
- Блоки, которым не нужны внешние переменные, оборачиваются в анонимный блок `{ ... }` —
  для ограничения области видимости и визуального разделения.

### Обработка ошибок при инициализации
- Используется хелпер `errCheck(err, msg)`: на этапе сборки любая ошибка фатальна
  (лог + `os.Exit(1)`). Ошибки из `Init` наверх не пробрасываются.
- Один заранее объявленный `var err error` переиспользуется по ходу `Init`.

### Парность Start / WaitJobs / Stop
- Для каждого фонового компонента, запускаемого в `Start()`, есть симметричный вызов в
  `WaitJobs()` (`.Wait()`) и/или в `Stop()`. **Порядок перечисления компонентов одинаков во
  всех трёх методах** — это упрощает чтение и сверку.

### Конфигурация и опциональные компоненты
- Все параметры берутся из единого глобального конфига (`config.Conf.*`) прямо в месте
  использования.
- Опциональные компоненты включаются по условию на конфиг (`if config.Conf.X != "" { ... }`),
  с фолбэком на in-memory/no-op реализацию через общий интерфейс.

---

## Runtime и конфигурация

### Запуск
- Entry: `cmd/main.go` → `internal/app.App`.
- На старте выполняются:
  - загрузка env (autoload `.env`) и yaml-правил (`RULES_PATH`),
  - настройка логгера/метрик,
  - pgx pool, миграции (`internal/app/migration.go`),
  - клиенты источников (недоступный источник не мешает старту — виден в `ping`),
  - индексер (`INDEXER_ENABLED`, `INDEXER_INTERVAL`),
  - MCP HTTP-сервер и системный HTTP-сервер.

### Переменные окружения
- Описаны в `internal/config/config.go`; пример — `.env.example`.
- Kubernetes: пустой `KUBECONFIG` — in-cluster; для локальной разработки `KUBECONFIG` + `KUBE_CONTEXT`.

### Системный HTTP-сервер
- Отдельный сервер на `SYSTEM_HTTP_PORT` (дефолт `3003`).
- Обслуживает служебные ручки: `/healthcheck`, `/readiness` (Postgres доступен), `/docs/*`, `/metrics`.

### Метрики
- Prometheus метрики на `/metrics` (системный сервер) при `WITH_METRICS=true`.
  Используется `metrics.Registry`, а не дефолтный `promhttp.Handler()`.

### Сборка
- `make build` создаёт бинарник `cmd/build/svc` (версия — `-X internal/constant.Version`).
- Dockerfile копирует бинарник, `docs/`, `migrations/` и `conf.example.yml` (как `conf.yml`) в `/app`.

### Flow проверки изменений
```
gofmt  →  go vet ./...  →  go test ./...  →  golangci-lint run  →  запуск на тестовом стенде
```

---

## Тестовый стенд

- Postgres только в docker:
  ```
  docker run --rm -d --name pulse-pg -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres \
    -e POSTGRES_DB=pulse -p 5440:5432 postgres:17
  ```
  DSN: `postgres://postgres:postgres@localhost:5440/pulse?sslmode=disable`.
- Порт 3003 на машине может быть занят другим проектом — для pulse: `SYSTEM_HTTP_PORT=3013`,
  `HTTP_PORT=9091`, `MCP_AUTH_TOKEN=devtoken`, `RULES_PATH=./conf.example.yml`.
- Кластер для прогонов индексера — локальный docker-desktop (`KUBECONFIG=$HOME/.kube/config
  KUBE_CONTEXT=docker-desktop CLUSTER_NAME=local`). Прод (`yc-zeon`) — только read-only и только
  по явной просьбе.
- Вызов инструмента через curl (ответ — SSE, строка `data: {...}`):
  ```
  curl -s -X POST localhost:9091/mcp -H "Authorization: Bearer devtoken" \
    -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
    -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ping","arguments":{}}}'
  ```
- Живой тест registry-клиента: `REGISTRY_LIVE_IMAGE=ghcr.io/actions/actions-runner:latest go test ./internal/service/registry/... -run TestLive -v`.
