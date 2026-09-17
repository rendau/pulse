-- Каталог топологии: полностью производный от кластера и service.yaml.
-- Значений метрик, логов и счётчиков здесь нет (Р4): только связи и метаданные.

create table service (
    name             text primary key,
    title            text        not null default '',
    repo_url         text        not null default '',
    description      text        not null default '',
    criticality      text        not null default '',
    owner_team       text        not null default '',
    owner_contacts   text[]      not null default '{}',
    aliases          text[]      not null default '{}',
    metadata_present boolean     not null default false,
    -- разобранный service.yaml (метрики, логи, раннбуки, endpoints)
    metadata         jsonb       not null default '{}',
    first_seen       timestamptz not null default now(),
    last_seen        timestamptz not null default now()
);

create index service_repo_url_idx on service (repo_url);
create index service_owner_team_idx on service (owner_team);

create table workload (
    cluster          text        not null,
    namespace        text        not null,
    kind             text        not null,
    name             text        not null,
    service_name     text        not null,
    replicas_desired integer     not null default 0,
    -- image из спеки контейнера; image_digest — что реально запущено (из статуса пода)
    image            text        not null default '',
    image_digest     text        not null default '',
    deployed_commit  text        not null default '',
    -- label-селектор подов workload'а, чтобы брать их состояние живьём
    selector         text        not null default '',
    -- имена configmap/secret шаблона пода (envFrom, env valueFrom, volumes), без значений
    config_refs      text[]      not null default '{}',
    first_seen       timestamptz not null default now(),
    last_seen        timestamptz not null default now(),
    primary key (cluster, namespace, kind, name)
);

create index workload_service_name_idx on workload (service_name);
create index workload_last_seen_idx on workload (last_seen);

-- История деплоев: смена образа/digest у workload между циклами индексера (фаза 4).
create table deploy (
    id                bigserial primary key,
    cluster           text        not null,
    namespace         text        not null,
    kind              text        not null,
    name              text        not null,
    service_name      text        not null,
    image             text        not null default '',
    image_digest      text        not null default '',
    deployed_commit   text        not null default '',
    prev_image        text        not null default '',
    prev_image_digest text        not null default '',
    prev_commit       text        not null default '',
    observed_at       timestamptz not null default now()
);

create index deploy_service_observed_idx on deploy (service_name, observed_at desc);
create index deploy_observed_idx on deploy (observed_at desc);

-- Сконфигурированные связи между сервисами (фаза 5): из env/configmap подов, позже — kusec.
-- Это связи по конфигурации, а не фактический трафик.
create table dependency (
    cluster      text        not null,
    from_service text        not null,
    -- to_service пустой — внешний адрес (не резолвится в сервис каталога)
    to_service   text        not null default '',
    to_host      text        not null,
    port         integer     not null default 0,
    scheme       text        not null default '',
    -- source — env | configmap | kusec; key — имя переменной/ключа, откуда взята связь
    source       text        not null,
    key          text        not null,
    first_seen   timestamptz not null default now(),
    last_seen    timestamptz not null default now(),
    primary key (cluster, from_service, to_host, port, key)
);

create index dependency_to_service_idx on dependency (to_service);
create index dependency_last_seen_idx on dependency (last_seen);
