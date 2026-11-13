create table if not exists cron_checkpoint
(
    job_name text primary key,
    last_run timestamptz not null default '1970-01-01'
);

COMMENT ON TABLE cron_checkpoint IS
    'Вспомогательная таблица: хранит метку времени последнего успешного запуска каждого cron-job. '
        'Используется для инкрементальной обработки в job recalculate_task_stats и recalculate_topic_score: '
        'обрабатываются только строки с updated_at / stats_updated_at >= last_run. '
        'Обновляется в конце job только при успешном завершении — '
        'при ошибке checkpoint не обновляется, следующий запуск повторит весь диапазон.';

COMMENT ON COLUMN cron_checkpoint.job_name IS 'Имя cron-job (совпадает с jobname в cron.job). Первичный ключ.';
COMMENT ON COLUMN cron_checkpoint.last_run IS 'Метка времени последнего успешного завершения job. При первом запуске = ''1970-01-01'' — гарантирует полный начальный пересчёт.';

insert into cron_checkpoint (job_name)
values ('recalculate_task_stats'),
       ('recalculate_topic_score')
on conflict do nothing;

create table if not exists cron_alerts
(
    id         bigserial primary key,
    created_at timestamptz not null default now(),
    job_name   text,
    run_id     bigint,
    status     text,
    return_msg text
);

COMMENT ON TABLE cron_alerts IS
    'Таблица алертов о падении cron-job. '
        'Заполняется job monitor_cron_jobs (каждый час в :05) из cron.job_run_details. '
        'Содержит только записи со status = ''failed'' за последние 25 часов, без дублей. '
        'Внешний мониторинг (pg_notify или скрипт) опрашивает эту таблицу для оповещений.';

COMMENT ON COLUMN cron_alerts.id IS 'Bigserial — суррогатный первичный ключ алерта.';
COMMENT ON COLUMN cron_alerts.created_at IS 'Время записи алерта в таблицу (не время падения job — время детектирования).';
COMMENT ON COLUMN cron_alerts.job_name IS 'Имя упавшего cron-job (jobname из cron.job).';
COMMENT ON COLUMN cron_alerts.run_id IS 'Идентификатор запуска из cron.job_run_details. Используется для дедупликации: повторная вставка того же runid предотвращается условием в запросе monitor_cron_jobs.';
COMMENT ON COLUMN cron_alerts.status IS 'Статус запуска из cron.job_run_details. Всегда ''failed'' для записей в этой таблице.';
COMMENT ON COLUMN cron_alerts.return_msg IS 'Текст ошибки из cron.job_run_details.return_message. Используется для диагностики причины падения job.';

-- Job 1: recalculate_task_stats
select cron.unschedule('recalculate_task_stats')
where exists (select 1 from cron.job where jobname = 'recalculate_task_stats');

select cron.schedule(
               'recalculate_task_stats',
               '0 3 * * *',
               $$ do $exec$
    declare
        v_last_run  timestamptz;
        v_now       timestamptz := now();
        v_offset    int         := 0;
        v_batch     int         := 5000;
        v_updated   int;
    begin
        select last_run into v_last_run from cron_checkpoint where job_name = 'recalculate_task_stats';

        create temp table _changed_tasks on commit drop as
        select distinct task_id from user_task where updated_at >= v_last_run
        union
        select distinct task_id from user_room_test_task where updated_at >= v_last_run;

        loop
            with batch as (select task_id from _changed_tasks order by task_id limit v_batch offset v_offset),
                 s as (select ut.task_id, round(count(*) filter (where ut.is_correct = true)::numeric / nullif(count(*), 0) * 100, 2) as solve_rate, round(avg(ut.total_attempts), 2) as avg_attempts, hll_cardinality(hll_add_agg(hll_hash_bigint(ut.user_id)))::int as bank_stats_count from user_task ut join batch b on b.task_id = ut.task_id group by ut.task_id),
                 ts as (select urtt.task_id, round(avg(urtt.time_to_solve))::int as avg_solve_time, hll_cardinality(hll_add_agg(hll_hash_bigint(urtt.user_id)))::int as test_stats_count from user_room_test_task urtt join batch b on b.task_id = urtt.task_id group by urtt.task_id),
                 merged as (select coalesce(s.task_id, ts.task_id) as task_id, s.solve_rate, s.avg_attempts, s.bank_stats_count, ts.avg_solve_time, ts.test_stats_count from s full outer join ts on ts.task_id = s.task_id)
            update task t set solve_rate = m.solve_rate, avg_attempts = m.avg_attempts, bank_stats_count = m.bank_stats_count, avg_solve_time = m.avg_solve_time, test_stats_count = m.test_stats_count from merged m where t.id = m.task_id;

            get diagnostics v_updated = row_count;
            exit when v_updated < v_batch;
            v_offset := v_offset + v_batch;
            perform pg_sleep(0.05);
        end loop;

        update cron_checkpoint set last_run = v_now where job_name = 'recalculate_task_stats';
    exception when others then
        raise warning 'recalculate_task_stats failed: %', sqlerrm;
        raise;
    end;
    $exec$ $$
);

-- Job 2: recalculate_topic_score
select cron.unschedule('recalculate_topic_score')
where exists (select 1 from cron.job where jobname = 'recalculate_topic_score');

select cron.schedule(
    'recalculate_topic_score',
    '30 3 * * *',
    $$ do $exec$
    declare
        v_last_run      timestamptz;
        v_now           timestamptz := now();
        v_normalizer    numeric;
    begin
        select last_run into v_last_run from cron_checkpoint where job_name = 'recalculate_topic_score';

        select coalesce(max((100.0 - success_rate) * ln(attempted + 1)), 1) into v_normalizer
        from user_topic_stat where attempted > 0;

        update user_topic_stat uts
        set score = round(least((100.0 - uts.success_rate) * ln(uts.attempted + 1) / nullif(v_normalizer, 0) * 100, 100), 2),
            stats_updated_at = v_now
        where uts.stats_updated_at >= v_last_run;

        update cron_checkpoint set last_run = v_now where job_name = 'recalculate_topic_score';
    exception when others then
        raise warning 'recalculate_topic_score failed: %', sqlerrm;
        raise;
    end;
    $exec$ $$
       );

-- Job 3: partman_maintenance
select cron.unschedule('partman_maintenance')
where exists (select 1 from cron.job where jobname = 'partman_maintenance');

select cron.schedule(
               'partman_maintenance',
               '0 2 * * *',
               $$ do $exec$
    declare
        v_retention text;
    begin
        select retention::text into v_retention from partman.part_config where parent_table = 'public.user_task_event';
        if v_retention is null then
            raise warning 'partman_maintenance: retention не задан для user_task_event.';
        end if;
        perform partman.run_maintenance('public.user_task_event');
        analyze public.user_task_event;
    exception when others then
        raise warning 'partman_maintenance failed: %', sqlerrm;
        raise;
    end;
    $exec$ $$
);

-- Job 4: monitor_cron_jobs
select cron.unschedule('monitor_cron_jobs')
where exists (select 1 from cron.job where jobname = 'monitor_cron_jobs');

select cron.schedule(
    'monitor_cron_jobs',
    '5 * * * *',
    $$
                   insert into cron_alerts (job_name, run_id, status, return_msg)
       select jobname, runid, status, return_message from cron.job_run_details
       where status = 'failed' and start_time > now() - interval '25 hours'
       and runid not in (select run_id from cron_alerts where run_id is not null);
$$
);