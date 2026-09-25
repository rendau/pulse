alter table workload drop column if exists manifest cascade;
alter table workload drop column if exists manifest_checked_at cascade;
alter table workload drop column if exists manifest_digest cascade;
alter table workload drop column if exists manifest_tried cascade;
alter table workload drop column if exists manifest_port cascade;
alter table workload drop column if exists manifest_reasons cascade;
alter table workload drop column if exists manifest_status cascade;
