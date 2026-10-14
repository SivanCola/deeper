-- Only the Edge Function's service role can submit or inspect reports.
create table public.deepzora_reports (
    event_id text primary key check (event_id ~ '^[0-9a-f]{32}$'),
    dedup_key text not null check (dedup_key ~ '^[0-9a-f]{64}$'),
    kind text not null check (kind in ('crash', 'exception', 'feedback', 'performance', 'bot')),
    schema_version integer not null check (schema_version in (2, 3, 4)),
    version text not null,
    source text not null default '',
    os text not null,
    arch text not null,
    received_at timestamptz not null default now(),
    payload jsonb not null check (jsonb_typeof(payload) = 'object' and octet_length(payload::text) <= 524288)
);
create index deepzora_reports_received_at_idx on public.deepzora_reports (received_at);
create index deepzora_reports_group_idx on public.deepzora_reports (dedup_key, received_at desc);

-- A database budget applies across all Edge Function instances. No IPs stored.
create table public.deepzora_report_budget (
    minute timestamptz primary key,
    accepted integer not null default 0 check (accepted between 0 and 120)
);

alter table public.deepzora_reports enable row level security;
alter table public.deepzora_report_budget enable row level security;
revoke all on public.deepzora_reports, public.deepzora_report_budget from public, anon, authenticated;
grant select, insert, delete on public.deepzora_reports to service_role;
grant select, insert, update, delete on public.deepzora_report_budget to service_role;

create function public.ingest_deepzora_report(p_report jsonb)
returns jsonb
language plpgsql
security invoker
set search_path = ''
as $$
declare
    current_minute timestamptz := date_trunc('minute', statement_timestamp());
    report_event_id text := p_report ->> 'eventId';
    current_count integer;
    inserted_id text;
begin
    -- Do not expose this RPC to clients. Validate identifiers again at storage.
    if jsonb_typeof(p_report) is distinct from 'object'
        or report_event_id is null or report_event_id !~ '^[0-9a-f]{32}$'
        or (p_report ->> 'dedupKey') is null
        or (p_report ->> 'dedupKey') !~ '^[0-9a-f]{64}$' then
        raise exception 'invalid report identity' using errcode = '22023';
    end if;

    insert into public.deepzora_report_budget (minute) values (current_minute)
        on conflict do nothing;
    select accepted into current_count from public.deepzora_report_budget
        where minute = current_minute for update;

    -- A retry after a lost response succeeds without changing the stored event
    -- or consuming the budget. dedup_key groups incidents; it is not unique.
    if exists (select 1 from public.deepzora_reports r where r.event_id = report_event_id) then
        return jsonb_build_object('status', 'duplicate');
    end if;
    if current_count >= 120 then
        return jsonb_build_object('status', 'rate_limited');
    end if;

    insert into public.deepzora_reports (
        event_id, dedup_key, kind, schema_version, version, source, os, arch, payload
    ) values (
        report_event_id, p_report ->> 'dedupKey', p_report ->> 'kind',
        (p_report ->> 'schemaVersion')::integer, p_report ->> 'version',
        coalesce(p_report ->> 'source', ''), p_report ->> 'os', p_report ->> 'arch', p_report
    ) on conflict do nothing returning deepzora_reports.event_id into inserted_id;
    if inserted_id is null then
        return jsonb_build_object('status', 'duplicate');
    end if;
    update public.deepzora_report_budget set accepted = accepted + 1 where minute = current_minute;
    delete from public.deepzora_report_budget where minute < current_minute - interval '10 minutes';
    return jsonb_build_object('status', 'accepted');
end;
$$;

revoke all on function public.ingest_deepzora_report(jsonb) from public, anon, authenticated;
grant execute on function public.ingest_deepzora_report(jsonb) to service_role;
