-- Манифест сервиса (docs/service-manifest.md): результат поиска на подах workload'а. Пишет
-- индексер; сам манифест хранится как получен (принятый, JSON) и разбирается каждый цикл.
alter table workload add column if not exists manifest_status     text        not null default '';
alter table workload add column if not exists manifest_reasons    text[]      not null default '{}';
alter table workload add column if not exists manifest_port       integer     not null default 0;
alter table workload add column if not exists manifest_tried      text[]      not null default '{}';
alter table workload add column if not exists manifest_digest     text        not null default '';
alter table workload add column if not exists manifest_checked_at timestamptz;
alter table workload add column if not exists manifest            jsonb;
