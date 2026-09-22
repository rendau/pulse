-- Имена, под которыми сервис известен в кластере, кроме имени каталога: workload'ы,
-- k8s Service (по селектору) и приложения gateway ruto. Пишет индексер; по ним
-- resolve_service находит orders-center по «ocenter».
alter table service add column if not exists cluster_names text[] not null default '{}';
