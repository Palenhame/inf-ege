create table task_recommendation_log
(
    id         serial primary key,
    user_id    uuid                  not null,
    foreign key (user_id) references "user" (id) on delete cascade,
    task_id    int                   not null,
    foreign key (task_id) references task (id) on delete cascade,
    reason_id  recommendation_reason not null,
    was_solved boolean               null,
    created_at timestamptz           not null default now()
);

COMMENT ON TABLE task_recommendation_log IS
    'Лог выданных рекомендаций задач пользователям. '
    'Запись создаётся в момент выдачи рекомендации. '
    'was_solved обновляется асинхронно: после того как пользователь взаимодействует с задачей. '
    'Используется для оценки качества алгоритма рекомендаций в разрезе reason_id, '
    'отладки пайплайна и A/B-тестирования стратегий.';

COMMENT ON COLUMN task_recommendation_log.id IS 'Суррогатный первичный ключ (serial).';
COMMENT ON COLUMN task_recommendation_log.user_id IS 'FK на пользователя, которому выдана рекомендация. При удалении — каскадное удаление.';
COMMENT ON COLUMN task_recommendation_log.task_id IS 'FK на рекомендованную задачу. При удалении задачи — каскадное удаление.';
COMMENT ON COLUMN task_recommendation_log.reason_id IS 'Стратегия рекомендации: weak_topic / similar_task / adaptive_difficulty. Позволяет анализировать эффективность каждой стратегии отдельно.';
COMMENT ON COLUMN task_recommendation_log.was_solved IS 'Результат взаимодействия пользователя с рекомендацией. true  — пользователь решил задачу после рекомендации. false — пользователь открыл задачу, но не решил. NULL  — пользователь проигнорировал рекомендацию (не открыл).';
COMMENT ON COLUMN task_recommendation_log.created_at IS 'Время выдачи рекомендации.';

create index rec_log_user_id_index on task_recommendation_log (user_id);
COMMENT ON INDEX rec_log_user_id_index IS 'Btree-индекс по user_id. Ускоряет выборку истории рекомендаций конкретного пользователя.';

create index rec_log_created_at_index on task_recommendation_log (created_at desc);
COMMENT ON INDEX rec_log_created_at_index IS 'Btree-индекс по created_at DESC. Ускоряет выборку последних рекомендаций и очистку устаревших записей.';