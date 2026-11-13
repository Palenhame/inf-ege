create table user_topic_stat
(
    user_id          uuid          not null,
    foreign key (user_id) references "user" (id) on delete cascade,
    topic_id         int           not null,
    foreign key (topic_id) references topic (id) on delete cascade,
    solved           int           not null default 0 check ( solved >= 0 ),
    attempted        int           not null default 0 check ( attempted >= 0 ),
    success_rate     numeric(5, 2) not null default 0 check ( success_rate >= 0 and success_rate <= 100 ),
    avg_time         integer       not null default 0 check ( avg_time >= 0 ),
    score            numeric(5, 2) not null default 0 check ( score >= 0 and score <= 100 ),
    updated_at       timestamptz   not null default now(),
    stats_updated_at timestamptz   not null default now(),
    created_at       timestamptz   not null default now(),
    primary key (user_id, topic_id)
);

COMMENT ON TABLE user_topic_stat IS
    'Денормализованная агрегированная статистика пользователя по теме. '
    'Одна запись на пару (user_id, topic_id). '
    'Является центральным источником данных для рекомендательной системы: '
    'слабые темы (низкий score) приоритизируются алгоритмом weak_topic. '
    'solved/attempted/success_rate/avg_time обновляются триггером sync_user_topic_stat() реалтайм. '
    'score пересчитывается ежедневным cron-job recalculate_topic_score (03:30). '
    'Денормализация намеренная — исключает агрегационные JOIN при запросах рекомендаций.';

COMMENT ON COLUMN user_topic_stat.user_id IS 'FK на пользователя. Вместе с topic_id образует составной PRIMARY KEY.';
COMMENT ON COLUMN user_topic_stat.topic_id IS 'FK на тему. Вместе с user_id образует составной PRIMARY KEY.';
COMMENT ON COLUMN user_topic_stat.solved IS 'Количество уникальных задач темы, решённых пользователем правильно хотя бы один раз (is_correct = true в user_task или user_room_test_task). Обновляется триггером sync_user_topic_stat().';
COMMENT ON COLUMN user_topic_stat.attempted IS 'Количество уникальных задач темы, которые пользователь хотя бы раз пытался решить. Обновляется триггером sync_user_topic_stat().';
COMMENT ON COLUMN user_topic_stat.success_rate IS 'Точность пользователя по теме: solved / attempted * 100 (0–100). Отражает долю задач, решённых хотя бы раз правильно. Обновляется триггером sync_user_topic_stat().';
COMMENT ON COLUMN user_topic_stat.avg_time IS 'Среднее время решения одной задачи темы в секундах. Источник: user_room_test_task.time_to_solve (только тесты — банк задач время не фиксирует). Обновляется триггером sync_user_topic_stat().';
COMMENT ON COLUMN user_topic_stat.score IS 'Итоговый балл слабости темы для ранжирования рекомендаций (0–100). Формула: (100 - success_rate) * ln(attempted + 1) / normalizer * 100, ограничено 100. Чем ниже точность и чем больше попыток — тем выше score (тема слабее). Нормализатор вычисляется динамически по всем активным строкам. Строки с attempted = 0 получают score = 0 (cold start — отдельная логика в рекомендациях). Пересчитывается cron-job recalculate_topic_score (03:30 ежедневно).';
COMMENT ON COLUMN user_topic_stat.updated_at IS 'Время последнего изменения записи. Обновляется триггером user_topic_stat_updated_at.';
COMMENT ON COLUMN user_topic_stat.stats_updated_at IS 'Время последнего обновления solved/attempted/success_rate/avg_time триггером. Используется cron-job recalculate_topic_score как маркер инкрементальной обработки: пересчитывается score только для строк с stats_updated_at >= last_run.';
COMMENT ON COLUMN user_topic_stat.created_at IS 'Время первого появления статистики пользователя по теме.';

create index user_topic_stat_score_index on user_topic_stat (score);
COMMENT ON INDEX user_topic_stat_score_index IS 'Btree-индекс по score. Ускоряет выборку тем с наибольшим score (слабые темы пользователя) для рекомендательной системы (ORDER BY score DESC).';

create trigger user_topic_stat_updated_at
    before update on user_topic_stat
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER user_topic_stat_updated_at ON user_topic_stat IS
    'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at(). '
    'Примечание: stats_updated_at обновляется явно в sync_user_topic_stat(), не через этот триггер.';