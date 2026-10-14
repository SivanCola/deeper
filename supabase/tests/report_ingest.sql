-- Run only against a disposable local database; all changes are rolled back.
begin;
do $$
begin
    if current_database() <> 'deepzora_report_test' then
        raise exception 'use the disposable deepzora_report_test database';
    end if;
    if has_table_privilege('anon', 'public.deepzora_reports', 'SELECT')
        or has_table_privilege('authenticated', 'public.deepzora_reports', 'INSERT')
        or has_function_privilege('anon', 'public.ingest_deepzora_report(jsonb)', 'EXECUTE')
        or has_function_privilege('authenticated', 'public.ingest_deepzora_report(jsonb)', 'EXECUTE') then
        raise exception 'reports or ingest RPC exposed to public clients';
    end if;
    if has_table_privilege('anon', 'public.deepzora_report_ingest_settings', 'SELECT')
        or has_table_privilege('authenticated', 'public.deepzora_report_ingest_settings', 'SELECT')
        or has_function_privilege('anon', 'public.submit_deepzora_report(jsonb,text)', 'EXECUTE')
        or has_function_privilege('authenticated', 'public.submit_deepzora_report(jsonb,text)', 'EXECUTE') then
        raise exception 'token configuration or authenticated RPC exposed to clients';
    end if;
    if not (select relrowsecurity from pg_class where oid = 'public.deepzora_reports'::regclass)
        or not (select relrowsecurity from pg_class where oid = 'public.deepzora_report_budget'::regclass) then
        raise exception 'RLS not enabled';
    end if;
end;
$$;

-- Test missing configuration before provisioning a digest as the DB owner.
do $$
begin
    if public.submit_deepzora_report('{}', repeat('1', 64)) ->> 'status' <> 'not_configured' then
        raise exception 'unconfigured collector did not fail closed';
    end if;
end;
$$;
insert into public.deepzora_report_ingest_settings (id, token_sha256) values (1, repeat('1', 64));

set local role service_role;
do $$
declare
    report jsonb := jsonb_build_object(
        'eventId', repeat('a', 32), 'dedupKey', repeat('b', 64),
        'kind', 'feedback', 'schemaVersion', 4, 'version', 'test',
        'os', 'darwin', 'arch', 'arm64', 'message', 'test-only feedback'
    );
    result jsonb;
begin
    result := public.submit_deepzora_report(report, repeat('2', 64));
    if result ->> 'status' <> 'unauthorized' then raise exception 'invalid token accepted'; end if;
    if exists (select 1 from public.deepzora_reports)
        or exists (select 1 from public.deepzora_report_budget) then
        raise exception 'unauthorized report mutated storage or rate budget';
    end if;
    result := public.submit_deepzora_report(report, repeat('1', 64));
    if result ->> 'status' <> 'accepted' then raise exception 'feedback not accepted'; end if;
    result := public.ingest_deepzora_report(report || '{"message":"retry must not overwrite"}');
    if result ->> 'status' <> 'duplicate' then raise exception 'retry not idempotent'; end if;
    if (select payload ->> 'message' from public.deepzora_reports where event_id = repeat('a', 32)) <> 'test-only feedback' then
        raise exception 'retry overwrote original event';
    end if;
    result := public.ingest_deepzora_report(report || jsonb_build_object(
        'eventId', repeat('c', 32), 'kind', 'crash', 'schemaVersion', 2, 'source', 'cli.go'
    ));
    if result ->> 'status' <> 'accepted' then raise exception 'CLI crash not accepted'; end if;
    if (select count(*) from public.deepzora_reports where dedup_key = repeat('b', 64)) <> 2 then
        raise exception 'similar incidents were collapsed';
    end if;
    if (select accepted from public.deepzora_report_budget where minute = date_trunc('minute', statement_timestamp())) <> 2 then
        raise exception 'retry consumed rate budget';
    end if;
    for i in 1..118 loop
        result := public.ingest_deepzora_report(report || jsonb_build_object('eventId', md5(i::text)));
        if result ->> 'status' <> 'accepted' then raise exception 'budget exhausted early'; end if;
    end loop;
    result := public.ingest_deepzora_report(report || jsonb_build_object('eventId', repeat('d', 32)));
    if result ->> 'status' <> 'rate_limited' then raise exception 'rate budget not enforced'; end if;
    if (select count(*) from public.deepzora_reports) <> 120 then raise exception 'rate-limited event was persisted'; end if;
    result := public.ingest_deepzora_report(report);
    if result ->> 'status' <> 'duplicate' then raise exception 'retry rejected at full budget'; end if;
end;
$$;
rollback;
