create table user_task
(
    id                        serial primary key,
    user_id                   uuid        not null,
    foreign key (user_id) references "user" (id) on delete cascade,
    task_id                   int         not null,
    foreign key (task_id) references task (id) on delete cascade,
    topic_id                  int         not null,
    foreign key (topic_id) references topic (id) on delete cascade,
    is_correct                boolean     not null default false,
    total_attempts            int         not null default 0 check ( total_attempts >= 0 ),
    attempts_since_last_solve int         not null default 0 check ( attempts_since_last_solve >= 0 ),
    histogram_updated_at      timestamptz null,
    user_answer               text        null,
    first_solved_at           timestamptz null,
    last_solved_at            timestamptz null,
    updated_at                timestamptz not null default now(),
    created_at                timestamptz not null default now(),
    unique (user_id, task_id)
);

COMMENT ON TABLE user_task IS
    'Агрегированное состояние решения задачи из банка конкретным пользователем. '
    'Одна запись на пару (user_id, task_id) — создаётся при первой попытке, '
    'обновляется при каждой последующей. '
    'Является основным источником данных для расчёта solve_rate и avg_attempts в task. '
    'Также является источником для sync_user_topic_stat() при работе с банком задач. '
    'topic_id денормализован из task, чтобы исключить JOIN при агрегации по темам.';

COMMENT ON COLUMN user_task.id IS 'Суррогатный первичный ключ (serial). Уникальность пары (user_id, task_id) обеспечивается отдельным UNIQUE.';
COMMENT ON COLUMN user_task.user_id IS 'FK на пользователя. При удалении пользователя все его записи удаляются каскадно.';
COMMENT ON COLUMN user_task.task_id IS 'FK на задачу. При удалении задачи все записи удаляются каскадно.';
COMMENT ON COLUMN user_task.topic_id IS 'Денормализованный FK на тему задачи (копия task.topic_id). Позволяет sync_user_topic_stat() агрегировать данные по теме без JOIN к task.';
COMMENT ON COLUMN user_task.is_correct IS 'true если пользователь решил задачу правильно хотя бы один раз за всё время. Используется для расчёта solve_rate в task (cron-job) и в рекомендательной системе.';
COMMENT ON COLUMN user_task.total_attempts IS 'Суммарное количество попыток пользователя за всё время (правильных и неправильных). Используется для расчёта avg_attempts в task (cron-job).';
COMMENT ON COLUMN user_task.attempts_since_last_solve IS 'Количество попыток с момента последнего успешного решения. Инкрементируется при каждой попытке, сбрасывается в 0 при успехе. Именно это значение (а не total_attempts) записывается в task.attempts_histogram — отражает усилие текущей сессии без накопленной истории предыдущих решений.';
COMMENT ON COLUMN user_task.histogram_updated_at IS 'Момент последнего обновления task.attempts_histogram этим пользователем. NULL при первом решении. Повторное обновление возможно не ранее чем через 7 дней — защита от накрутки статистики и race condition между воркерами FastAPI BackgroundTask.';
COMMENT ON COLUMN user_task.user_answer IS 'Последний ответ пользователя (текст). Используется рекомендательной системой для анализа паттернов ошибок и подбора похожих задач.';
COMMENT ON COLUMN user_task.first_solved_at IS 'Момент первого успешного решения задачи. NULL если задача ещё не решена.';
COMMENT ON COLUMN user_task.last_solved_at IS 'Момент последнего успешного решения задачи. NULL если задача ещё не решена.';
COMMENT ON COLUMN user_task.updated_at IS 'Время последнего изменения записи. Обновляется триггером user_task_updated_at. Используется cron-job recalculate_task_stats для инкрементальной выборки изменённых задач.';
COMMENT ON COLUMN user_task.created_at IS 'Время первой попытки пользователя решить задачу.';

create index user_task_updated_at_index on user_task (updated_at desc);
COMMENT ON INDEX user_task_updated_at_index IS 'Btree-индекс по updated_at DESC. Используется cron-job recalculate_task_stats для инкрементальной выборки задач с активностью после last_run.';

create trigger user_task_updated_at
    before update on user_task
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER user_task_updated_at ON user_task IS 'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';

create trigger trg_sync_user_topic_stat_user_task
    after insert or update on user_task
    for each row execute function sync_user_topic_stat();

COMMENT ON TRIGGER trg_sync_user_topic_stat_user_task ON user_task IS
    'После каждого INSERT или UPDATE вызывает sync_user_topic_stat() для пересчёта '
    'денормализованной статистики пользователя по теме в user_topic_stat. '
    'Обеспечивает реалтайм-консистентность статистики при работе с банком задач.';