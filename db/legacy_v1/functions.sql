create or replace function set_updated_at()
    returns trigger as $$
begin
    new.updated_at = now();
    return new;
end;
$$ language plpgsql;

COMMENT ON FUNCTION set_updated_at() IS
    'Универсальная триггерная функция: устанавливает updated_at = now() перед каждым UPDATE. '
    'Используется всеми таблицами со служебным полем updated_at для audit-трейла и '
    'инкрементальной синхронизации в cron-заданиях (фильтрация по updated_at >= last_run).';

CREATE OR REPLACE FUNCTION sync_user_topic_stat()
    RETURNS TRIGGER AS $$
DECLARE
    _solved INT;
    _attempted NUMERIC;
    _success_rate NUMERIC;
    _avg_time INT;
BEGIN
    WITH all_attempts AS (SELECT task_id, bool_or(is_correct) as is_solved
                          FROM (SELECT task_id, is_correct
                                FROM user_task
                                WHERE user_id = NEW.user_id AND topic_id = NEW.topic_id
                                UNION ALL
                                SELECT task_id, is_correct
                                FROM user_room_test_task
                                WHERE user_id = NEW.user_id AND topic_id = NEW.topic_id) sub
                          GROUP BY task_id)
    SELECT count(*) FILTER (WHERE is_solved = true),
           count(*),
           coalesce((count(*) FILTER (WHERE is_solved = true)::NUMERIC / NULLIF(count(*), 0)) * 100, 0)
    INTO _solved, _attempted, _success_rate
    FROM all_attempts;

    SELECT coalesce(avg(time_to_solve), 0) ::INT
    INTO _avg_time
    FROM user_room_test_task
    WHERE user_id = NEW.user_id AND topic_id = NEW.topic_id;

    INSERT INTO user_topic_stat (user_id, topic_id, solved, attempted, success_rate, avg_time, stats_updated_at)
    VALUES (NEW.user_id, NEW.topic_id, _solved, _attempted, _success_rate, _avg_time, now())
    ON CONFLICT (user_id, topic_id) DO UPDATE SET solved = EXCLUDED.solved,
                                                  attempted = EXCLUDED.attempted,
                                                  success_rate = EXCLUDED.success_rate,
                                                  avg_time = EXCLUDED.avg_time,
                                                  updated_at = now(),
                                                  stats_updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION sync_user_topic_stat() IS
    'Триггерная функция: пересчитывает денормализованную статистику пользователя по теме '
    'в user_topic_stat после каждого INSERT/UPDATE в user_task или user_room_test_task. '
    'Алгоритм: объединяет попытки из обеих таблиц через UNION ALL, дедуплицирует по task_id '
    'через bool_or(is_correct), считает solved/attempted/success_rate. '
    'Среднее время (avg_time) берётся только из user_room_test_task — банк задач время не фиксирует. '
    'topic_id берётся из NEW.topic_id (денормализация в user_task/user_room_test_task), '
    'чтобы избежать JOIN к таблице task. '
    'Использует INSERT ... ON CONFLICT DO UPDATE для идемпотентности. '
    'Обновляет stats_updated_at — маркер для cron-job пересчёта score (job 2).';

create or replace function check_answer_count()
    returns trigger as $$
declare
    v_test record;
begin
    select is_homework, max_attempts
    into v_test
    from test
    where id = new.test_id;
    if not v_test.is_homework then
        if new.count_correct_answers + new.count_incorrect_answers > v_test.max_attempts then
            raise exception 'Answer count exceeds max_attempts limit for non-homework test';
        end if;
    end if;
    return new;
end;
$$ language plpgsql;

COMMENT ON FUNCTION check_answer_count() IS
    'Триггерная функция: проверяет, что сумма count_correct_answers + count_incorrect_answers '
    'не превышает max_attempts теста перед INSERT/UPDATE в user_room_test_task. '
    'Пропускает проверку если test.is_homework = true — для домашних работ лимит попыток не действует. '
    'Выбрасывает exception при нарушении, транзакция откатывается.';

CREATE OR REPLACE FUNCTION sync_test_task_stat()
    RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO test_task_stat (test_id, task_id, solved_count, attempted_count, avg_time, avg_attempts)
    select NEW.test_id,
           NEW.task_id,
           count(*) filter ( where user_room_test_task.is_correct = true ),
           count(*),
           coalesce(avg(user_room_test_task.time_to_solve), 0)::int,
           coalesce(avg(user_room_test_task.count_incorrect_answers + user_room_test_task.count_correct_answers), 0)
    from user_room_test_task
    where test_id = new.test_id and task_id = new.task_id
    group by NEW.test_id, NEW.task_id
    ON CONFLICT (test_id, task_id) DO update
        set solved_count    = excluded.solved_count,
            attempted_count = excluded.attempted_count,
            avg_time        = excluded.avg_time,
            avg_attempts    = excluded.avg_attempts;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION sync_test_task_stat() IS
    'Триггерная функция: пересчитывает агрегированную статистику по задаче в конкретном тесте '
    '(test_task_stat) после каждого INSERT/UPDATE в user_room_test_task. '
    'Пересчитывает solved_count, attempted_count, avg_time, avg_attempts по всем ученикам '
    'текущего теста и задачи. Использует INSERT ... ON CONFLICT DO UPDATE для идемпотентности. '
    'Источник данных для дашборда учителя.';