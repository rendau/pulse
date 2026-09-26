# CLAUDE.md

Руководство для Claude Code по работе с этим репозиторием. Go-сервис на Clean Architecture + DDD:
MCP-сервер (streamable HTTP, `github.com/modelcontextprotocol/go-sdk`), Postgres (pgx/mobone),
клиенты Kubernetes/GitHub/registry/Prometheus/Loki/Alertmanager.

Принятые решения (отклонения от первоначальной постановки и шаблона):
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
- `conf.example.yml` — пример yaml-правил (`RULES_PATH`, по умолчанию `./conf.yml`).
- `Dockerfile`, `Makefile` — сборка (`make build` подставляет версию через ldflags).
- `.env.example` — пример окружения.

### Внутренние пакеты (`internal/`)
- `internal/app/` — сборка приложения: серверы, миграции, метрики, трассировка, HTTP-gateway, DI.
  - `app.go` — граф зависимостей и запуск компонентов.
  - `mcp_server.go` — MCP-сервер, HTTP-транспорт на `MCP_PATH`, bearer-auth middleware.
  - `system_http_server.go` — системный HTTP-сервер (`SYSTEM_HTTP_PORT`, дефолт 3003):
    /healthcheck, /readiness, /metrics.
  - `migration.go` — запуск миграций из `migrations/`.
- `internal/config/` — `config.go` (env: адреса, токены, порты — **все адреса и токены источников
  только через env**) и `rules.go` (yaml-правила: `image_mapping`, `indexer.*`, `snapshot.*`,
  `metrics.*`).
- `internal/handler/` — транспортный слой.
  - `mcp/` — MCP-инструменты (`handler.go` — регистрация и описания, по файлу на группу).
  - `mcp/dto/` — преобразование usecase-моделей ↔ JSON-ответы инструментов (теги только тут).
- `internal/infra/httpx/` — единая фабрика http-клиентов (таймауты, лимиты; все клиенты только через неё).
- `internal/infra/pulsekit/` — манифест сервиса по стандарту (`docs/service-manifest.md`) для самого pulse:
  `Depend(id, kind, target, critical, check).Affects("…")` — зависимость, что она ломает, и её фоновая
  проверка (ручка состояния, своё сообщение вместо текста ошибки — `Describe`; `Problem` — свой статус;
  `NewPassive` — проверка по исходам настоящих вызовов для платных API), `Host` — хост из URL/DSN/gRPC-адреса (небезопасный
  target → `unknown`, без паники), `Gauge`/`GaugeTime` — показатели состояния, `Metric`, `ErrorPattern`,
  `Service.Runbooks`, `Handle[T]` — диагностическая ручка, схема ответа из Go-типа (json-теги,
  `pulse:"personal=…,maxLength=…,maxItems=…,enum=a|b,description=…"` — description последним), лог
  каждого вызова с `X-Pulse-Request-Id` (`RequestId(ctx)`), `CheckEndpoint` — сверка ответа со схемой
  для тестов сервиса; нарушение стандарта при объявлении — не паника: элемент не публикуется, причина —
  в лог и `Problems()`; длинный текст (100/500, описание ручки 1000) — `Warnings()`.
  Эталон пакета — gotemplate (`internal/infra/pulsekit`), копии в pulse и pulse_agent совпадают с ним
  файл в файл; только у pulse есть `pulse_test.go` (сверка с `ParseManifest`) — в сервисы не копируется.
  Тест сверяет манифест с `ParseManifest`. Манифест pulse — `app/manifest.go` (зависимости — источники,
  ручка `indexer_last_cycle`), коммит сборки — `constant.Commit` (Makefile, ldflags). Прообраз модуля
  gotemplate.
- `internal/util/` — `imageref` (разбор ссылок на образы), `fuzzy` (нечёткое сравнение), `podname` (регэксп и владелец подов workload'а по его виду),
  `window` (разбор окна), `tz` (часовой пояс ответов — Asia/Almaty, база поясов вшита в бинарник; все времена в DTO и в текстах summary/details идут через `tz.In`), `redact` (маскирование конфигурации/секретов/PII — с тестами; `redact.Text` — телефоны,
  email, карты в свободном тексте; `redact.ReplacePII` — те же правила поиска с своей заменой: строки
  логов при чтении из Loki и Kubernetes идут через `pii.Text` — карты маской, до паттернов и ответа;
  значение явного поля `"card":"…"` — целиком, с пробелами).
- `internal/usecase/` — usecase-слой (валидация, оркестрация сервисов и доменных сервисов):
  `system` (ping), `catalog` (resolve/list/info), `snapshot` (fan-out снапшота, query_metrics),
  `logs` (query_logs, top_errors), `timeline` (get_timeline, get_changes), `dependencies`
  (get_dependencies: обход графа в ширину с лимитом узлов, здоровье соседей по подам),
  `endpoints` (call_service_endpoint: ручки только из манифеста сервиса — allowlist по id, только
  GET прямо в готовый под workload'а на порт манифеста, параметры по pattern/enum/x-personal (значение
  к одному виду через `pii.Normalize`), ответ — проекция на схему манифеста (`projection.go`: необъявленное и
  не того типа вырезается — `dropped_fields`; x-personal — как есть с отметкой `personal_fields` путь → вид;
  строки по maxLength, массивы по maxItems/max_rows), ошибка ручки — только `error`, `request_id` =
  `X-Pulse-Request-Id`), `cluster` (get_cluster_health: ноды, поды
  по кластеру (под без workload'а каталога — к сервису по репозиторию образа или
  `app.kubernetes.io/managed-by`), Warning-события по причинам с сервисами их объектов, инфра-алерты = не привязанные к каталогу,
  метрики кластера с базовой линией, ошибки в логах всего кластера по сервисам — `log_errors`,
  сервисы, которые сами сообщают о проблеме, — `self_reported`), `publicapi` (get_public_api: приложения ruto сервиса по рёбрам
  индексера, маршруты из снапшота ruto, трафик из метрик gateway). Исключение из правила
  «usecase не ходит в соседний usecase»:
  `snapshot` берёт `top_errors` у `logs` через узкий порт `LogsI`, чтобы не дублировать
  селектор + выборку + агрегацию; так же `cluster` берёт `ClusterErrors` у `logs` (порт `LogsI`).
- `internal/domain/` — доменная модель, сервисы и репозитории.
  - `svc`, `workload` — каталог (Postgres); `snapshot` — детерминированные правила снапшота
    (health, summary_hints, базовая линия) без обращения к источникам; `event` — нормализованное
    событие и его нормализация из фактов кластера/деплоев (`event/service`);
    `logs` — нормализация строк и агрегация в паттерны (чистые функции); `deploy` — история
    деплоев (Postgres), пишется индексером при смене образа/digest (digest — с самого свежего
    запущенного пода образа шаблона; пока поды нового образа не запустились, в каталоге остаются
    прошлые образ/digest/коммит, деплой пишет следующий цикл, манифест не ищется — `indexer/service/run.go`); `dependency` — сконфигурированные
    связи «сервис → хост» (Postgres) и разбор адресов из значений конфигурации (`ParseEndpoints`,
    `ClusterHost`); `cluster` — правила здоровья кластера и подсказки (фаза 7.1).
  - `*/model/` — доменные структуры (entity).
  - `*/service/` — доменные сервисы (инварианты/логика).
  - `*/repo/` — репозитории.
  - `common/` — общие модели/утилиты/PG базовый репозиторий.
- `internal/service/` — сервисы (фоновые/инфраструктурные), для переиспользования или выделения логики:
  `k8s`, `github`, `registry`, `prometheus`, `loki`, `alertmanager`, `kusec` (клиенты источников,
  read-only; kusec — по контракту `docs/monitoring-api.md` проекта kusec, ключ scope=read_only),
  `pii` (персональные данные: **токенов pulse не выдаёт** — телефоны и email отдаются как есть, от
  модели их прячет pulse_agent (токены, ключ `PII_TOKEN_KEY` — у него); `Text` — учётные данные в
  адресах вырезаны, карты — `***` + 4 цифры; `Normalize` — персональный параметр ручки к одному виду;
  `SearchPattern` — телефон с «+» в query_logs → предфильтр и регэксп номера в любом написании,
  email — без регистра; правила поиска PII — общие с `redact.ReplacePII`),
  `ruto` (снапшот конфигурации gateway ruto-core, кэш по версии; секретные поля не разбираются),
  `svcproxy` (GET прямо в под по IP, без редиректов; локально — `ENDPOINT_CALL_MODE=k8s-proxy`
  через `k8s.ProxyGetPod`),
  `indexer` (фоновый обход кластера → каталог + история деплоев), `selfreport` (самоотчёт сервиса:
  ручка состояния до 3 готовых подов workload'ов с манифестом, худший под, зависимости — только
  объявленные, объекты — только из domain; общий для снапшота и `get_cluster_health.self_reported` —
  сервисы, чей самоотчёт не ok или устарел, с подсказками), `selfstatus` (ручка состояния
  сервиса `<manifest.path>/status` прямо с пода: проверка по стандарту, тексты через
  `pii.Text` — учётные данные вырезаны, карты маской, кэш `manifest.status_cache`).
- `internal/errs/` и `internal/constant/` — общие коды ошибок и константы.

---

## Архитектура: слои и зависимости

- **Transport** (`internal/handler/mcp/*`):
  - Работает только с DTO инструментов и usecase-интерфейсами.
  - Не обращается напрямую к репозиториям и сервисам.
  - Описание инструмента — часть продукта: когда выбирать / когда нет, что возвращает, 4–5 строк.
  - Общее число инструментов — не более 12–13; новые — объединять с существующими.
  `list_service_endpoints` из первоначальной постановки влит в `get_service_info` (`diagnostic_endpoints`). Сейчас 13
  инструментов — лимит исчерпан, новые только объединением с существующими.
- RBAC индексера и инструментов: get на pods/log (логи без Loki); get/list на nodes, deployments, statefulsets, daemonsets, cronjobs, jobs,
  replicasets, pods, events, configmaps, services; для `ENDPOINT_CALL_MODE=k8s-proxy` — get на services/proxy
  и pods/proxy (локально; в кластере pulse ходит в поды напрямую по IP).
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
- Манифест сервиса (`docs/service-manifest.md` — стандарт для команд): сервис на служебном порту
  отдаёт `/.well-known/pulse`. Индексер ищет его на готовом поде workload'а (`indexer/service/manifest.go`):
  порты по порядку — аннотация `pulse/port`, порт `/metrics` из целей Prometheus (`up{pod!=""}`),
  имена `system`/`http*` у контейнера или у k8s Service, выбирающего под (номер — из `targetPort`), `manifest.default_ports`, остальные TCP-порты кроме `skip_ports` (только
  после выкатки). Искать заново — при смене digest, принятый — раз в `refresh_after`, неудача — раз
  в `retry_after`. Результат — в колонках `workload.manifest_*` (статус ok/partial/invalid/absent/
  unreachable, причины, опробованные порты, сам манифест как получен — разбирается каждый цикл).
  Разбор и проверка по стандарту — `indexer/service/model/manifest.go` (`ParseManifest`: invalid —
  ошибка, partial — `Problems`); `200` без `pulse_manifest` — не манифест (`IsManifest`). Манифест
  важнее `service.yaml` (`metadata.source`), коммит сборки из манифеста — первым. Вызов пода —
  `svcproxy.GetPod` (IP пода; `ENDPOINT_CALL_MODE=k8s-proxy` — через pods/proxy, 503 прокси
  «error trying to reach service» — «нет ответа», а не HTTP-ответ). `200` на любой путь (SPA,
  большая страница, обрезанная по лимиту) — тоже не манифест (`LooksLikeManifest`).
  Снапшот (`snapshot/self.go`) опрашивает ручку состояния до 3 готовых подов workload'ов с
  манифестом и показывает худший (`self_reported`): зависимости — только объявленные в манифесте;
  не-ok → health degraded и подсказки «сервис сообщает: …»; отчёт старше 5 мин — `stale`.
  Метрики манифеста — добавка к golden signals (service.yaml — замена).
  Раздел `domain` манифеста (бизнес-смысл: ответственность и границы, объекты с форматом номера,
  статусами и `stuck_after`, типичные вопросы) — `Metadata.Domain`, отдаётся в `get_service_info`;
  `query_logs` без service по формату номера (`id_pattern`) подсказывает, чей это объект (`id_matches`).
  Ручка состояния может отдавать `entities` — счётчики объектов из domain по статусам и застрявшие
  (порог — `stuck_after` из domain); снапшот показывает только объявленные в domain и подсказывает
  «застряло N из M … в статусе …».
- Логи: селектор из `service.yaml` (`logs.selector`), иначе `logs.default_selector` из правил
  с плейсхолдерами `{namespace}`, `{pod_regex}`; запрос без привязки к сервису невозможен.
  `{pod_regex}` (логи и метрики снапшота) — `util/podname`: только поды workload'ов сервиса по
  правилам именования вида (Deployment `имя-<hash>-<5>`, StatefulSet `имя-N`, DaemonSet `имя-<5>`,
  CronJob `имя-<время>-<5>`; хэш и суффикс — в алфавите k8s без гласных, длинная основа обрезается
  до 58), семейство Job'ов — префиксом. Префикс «имя-» не годится: у `pulse` захватывал
  `pulse-agent-…`, `pulse-pg-0`. Под → workload в Go — тоже `podname` (`Owner`/`ObjectOwner`:
  по правилам, иначе самый длинный префикс; в снапшоте сервиса — только по правилам).
  Строка привязывается к workload'у сервиса по лейблу пода (`pod`, `kubernetes_pod_name`, …),
  у паттерна — `workloads`; `query_logs(workload=…)` сужает до одного workload'а.
  Поды Job'ов, которые создаёт оркестратор (managed-by сервиса или его образ), входят в логи
  сервиса группой по общему префиксу имён Job'ов (`lt-zeon-*`, `logs/sources.go`): префикс
  покрывает и удалённые поды, но не должен захватывать чужие поды namespace'а (`util/jobprefix`).
  Источник логов — Loki, а если его нет или он не ответил — Kubernetes API (`pods/log`,
  `logs/kubernetes.go`): живые поды сервиса, хвост контейнеров и прошлый запуск; `source` в ответе.
- Логи всего кластера (`logs/cluster.go`, селектор `logs.cluster_selector`, только Loki):
  `query_logs` без `service` — поиск `pattern` (номер заказа, id клиента; ≥3 символов) по всем
  логам (raw по умолчанию — «что по заказу 234115»; идентификатор — отдельно стоящим: `|=` и
  регэксп «рядом не буква и не цифра», у числа — и не точка; `_`/`-` — разделители). Без `window`/`end` — назад по суткам (`scan`): у Loki нет индекса по словам,
  любой поиск читает все логи окна, поэтому сутки за запросом (≈2 с), по одному, стоп — два пустых
  дня перед найденными следами, срок хранения (`logs.retention`) или бюджет (`logs.search_budget`,
  1m; не успел — отдаёт проверенное, `search_stop: budget`). `end` (дата — эти сутки, время —
  Asia/Almaty) работает и для логов сервиса. Строка → сервис по workload'у с самым длинным
  префиксом имени пода, в ответе `services` (где и сколько, по всей выборке max_lines), строки —
  последние, по порядку событий; `get_cluster_health.log_errors` — точный счётчик error-строк по подам
  (`count_over_time`, строгий `errorLineRe`: уровень полем или ERROR капсом) + самый частый
  паттерн из выборки, top `cluster.max_log_services`; дедлайн `get_cluster_health` 1m (за сутки
  счётчик ~15 с), окно ошибок — не больше потолка логов 24h. Паттерн без метасимволов уходит в Loki
  подстрокой (`|=`), с метасимволами — регэкспом (`|~`).
- Образ, который запускается только Job'ами оркестратора (код задач в своей репе, своего
  Deployment/CronJob нет), индексер заводит отдельным сервисом (`indexer/service/jobs.go`):
  workload `Job` — семейство Job'ов, имя — общий префикс их имён. Ошибки задач привязываются
  к этому сервису, а не к оркестратору; коммиты и деплои — из его репозитория.
- Поиск сервиса (`svc/service/resolve.go`): вся фраза сильнее отдельных слов, кириллица —
  латинскими вариантами (`fuzzy.Translit`, `matched_by: translit`), почти равные лидеры —
  `ambiguous`. Перевод и синонимы — дело модели (инструкции MCP), не pulse. Описание сервиса
  без service.yaml — описание и topics репозитория GitHub (`RepoInfo`, кэш 6 ч).
  В проде логи шлёт fluent-bit: лейблы `kubernetes_namespace_name`, `kubernetes_pod_name`.
- Метрики приложений (go-шаблон) в проде с префиксом `<ns>_<svc>_request_total` (у старых —
  `_request_count`) и `status=ok|error`: дефолтные golden signals ищут имя регэкспом `__name__`.
  Новый шаблон добавляет лейбл `code` (код ошибки ответа): `error_rate` — сбои (`code` пуст или
  `service_not_available`/`not_implemented`/`invalid_config`), `rejected_rate` — отказы по делу
  (у сервиса без `code` пуст). Подсказка снапшота: ошибки в метриках есть, error-логов нет —
  ошибки ответов пишутся не уровнем error, причины — query_logs без level.
  Сервисам, опубликованным в ruto, снапшот добавляет `public_*` по метрикам gateway (`{ruto_apps}`).

### Хранилища
- Postgres: только топология и метаданные (`service`, `workload`). Значений метрик, логов и
  счётчиков в БД быть не должно — всё состояние запрашивается живьём.
- Домен сущности «сервис каталога» лежит в `internal/domain/svc` (таблица `service`).
- **Имена таблиц всегда в единственном числе**, без plural: `usr`, `app`, `secret`, `item`
  (не `usrs`, `apps`, `secrets`, `items`). То же значение указывается в `TableName` репозитория.

### Миграции
- Файлы в `migrations/` в формате `NNNNNN_<name>.up.sql` / `.down.sql` (golang-migrate).
- В `down`-миграциях во всех командах `DROP` обязательно указывать `CASCADE`
  (напр. `drop table if exists <table> cascade;`).
- В `down` объекты удаляются в порядке, обратном `up` (с учётом внешних ключей).

### API (MCP)
- Инструменты регистрируются в `internal/handler/mcp/handler.go` через `addTool` с типизированными
  In/Out DTO (`internal/handler/mcp/dto`); схема выводится из json/jsonschema-тегов.
- Все инструменты read-only: `ToolAnnotations{ReadOnlyHint: true}`.
- Регистрация — через `addTool` (`handler/mcp/schema.go`), не `mcp.AddTool`: схема ответа допускает
  новые поля (без `additionalProperties: false`), иначе клиенты с запомненной схемой ломаются после
  деплоя. Поля ответа можно добавлять; удалять и переименовывать нельзя. Схема входа — строгая.
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
- Секреты не покидают сервис: значения секретов никогда не попадают в ответ. Любое значение
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
- Обслуживает служебные ручки: `/healthcheck`, `/readiness` (Postgres доступен), `/metrics`.

### Метрики
- Prometheus метрики на `/metrics` (системный сервер) при `WITH_METRICS=true`.
  Используется `metrics.Registry`, а не дефолтный `promhttp.Handler()`.

### Сборка
- `make build` создаёт бинарник `cmd/build/svc` (версия — `-X internal/constant.Version`).
- Dockerfile копирует бинарник, `migrations/` и `conf.example.yml` (как `conf.yml`) в `/app`.

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
- Живой прогон запросов логов кластера против Loki в docker (`docker run --rm -d --name pulse-loki
  -p 3110:3100 grafana/loki:3.4.2`, строки — через `/loki/api/v1/push`):
  `LOKI_LIVE_URL=http://localhost:3110 go test ./internal/usecase/logs/ -run TestLive -v`.
- Манифест на локальном кластере: демо-сервисы в namespace `pulse-test` (nginx отдаёт
  `internal/service/indexer/service/model/testdata/manifest.json` на порту `system` 3003 и 404 на
  `http` 8080), pulse с `ENDPOINT_CALL_MODE=k8s-proxy`; итог — `get_service_info orders-center`
  (`metadata_source: manifest`, `workloads[].manifest`). Убрать: `kubectl --context docker-desktop delete ns pulse-test`.
- Живой тест registry-клиента: `REGISTRY_LIVE_IMAGE=ghcr.io/actions/actions-runner:latest go test ./internal/service/registry/... -run TestLive -v`.
