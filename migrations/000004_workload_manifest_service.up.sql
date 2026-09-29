-- Манифест сервиса ищется через k8s Service workload'а (порт с именем system): имя Service, через
-- который pulse вызывает манифест, ручку состояния и диагностические ручки; manifest_port — порт
-- этого Service.
alter table workload add column if not exists manifest_service text not null default '';
