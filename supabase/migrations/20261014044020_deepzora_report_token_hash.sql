-- Keep the submission token out of hosted code and Edge Function secrets.
-- A SHA-256 digest of a random 256-bit token cannot be used at the HTTP endpoint.
create table public.deepzora_report_ingest_settings (
    id smallint primary key check (id = 1),
    token_sha256 text not null check (token_sha256 ~ '^[0-9a-f]{64}$'),
    updated_at timestamptz not null default now()
);
alter table public.deepzora_report_ingest_settings enable row level security;
revoke all on public.deepzora_report_ingest_settings from public, anon, authenticated;
grant select on public.deepzora_report_ingest_settings to service_role;

create function public.submit_deepzora_report(p_report jsonb, p_token_hash text)
returns jsonb
language plpgsql
security invoker
set search_path = ''
as $$
declare
    expected_hash text;
begin
    select token_sha256 into expected_hash from public.deepzora_report_ingest_settings where id = 1;
    if expected_hash is null then
        return jsonb_build_object('status', 'not_configured');
    end if;
    if p_token_hash is distinct from expected_hash then
        return jsonb_build_object('status', 'unauthorized');
    end if;
    return public.ingest_deepzora_report(p_report);
end;
$$;
revoke all on function public.submit_deepzora_report(jsonb, text) from public, anon, authenticated;
grant execute on function public.submit_deepzora_report(jsonb, text) to service_role;
