create table test_task_stat
(
    test_id         uuid          not null,
    foreign key (test_id) references test (id) on delete cascade,
    task_id         int           not null,
    foreign key (task_id) references task (id) on delete cascade,
    avg_time        integer       not null default 0 check ( avg_time >= 0 ),
    solved_count    int           not null default 0 check ( solved_count >= 0 ),
    attempted_count int           not null default 0 check ( attempted_count >= 0 ),
    avg_attempts    numeric(5, 2) not null default 0 check ( avg_attempts >= 0 ),
    updated_at      timestamptz   not null default now(),
    created_at      timestamptz   not null default now(),
    primary key (test_id, task_id)
);

COMMENT ON TABLE test_task_stat IS
    'Агрегированная статистика задачи в конкретном тесте. '
    'Одна запись на пару (test_id, task_id). '
    'Обновляется реалтайм триггером trg_sync_test_task_stat после каждого результата ученика. '
    'Источник данных для дашборда учителя: отображает успеваемость класса по каждой задаче. '
    'Денормализация — исключает агрегационные запросы к user_room_test_task при открытии дашборда.';

COMMENT ON COLUMN test_task_stat.test_id IS 'FK на тест. Вместе с task_id образует составной PRIMARY KEY. При удалении теста — каскадное удаление.';
COMMENT ON COLUMN test_task_stat.task_id IS 'FK на задачу. При удалении задачи — каскадное удаление.';
COMMENT ON COLUMN test_task_stat.avg_time IS 'Среднее время решения задачи в секундах среди всех учеников теста. Источник: user_room_test_task.time_to_solve.';
COMMENT ON COLUMN test_task_stat.solved_count IS 'Количество учеников, решивших задачу правильно (is_correct = true).';
COMMENT ON COLUMN test_task_stat.attempted_count IS 'Количество учеников, хотя бы раз пытавшихся решить задачу.';
COMMENT ON COLUMN test_task_stat.avg_attempts IS 'Среднее количество попыток (correct + incorrect) на задачу среди всех учеников теста.';
COMMENT ON COLUMN test_task_stat.updated_at IS 'Время последнего пересчёта. Обновляется триггером test_task_stat_updated_at.';
COMMENT ON COLUMN test_task_stat.created_at IS 'Время первого появления статистики (после первого результата ученика).';

create trigger test_task_stat_updated_at
    before update on test_task_stat
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER test_task_stat_updated_at ON test_task_stat IS 'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';