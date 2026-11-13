create table user_room_test_task
(
    id                      serial primary key,
    user_id                 uuid        not null,
    foreign key (user_id) references "user" (id) on delete restrict,
    room_id                 uuid        not null,
    foreign key (room_id) references room (id) on delete restrict,
    task_id                 int         not null,
    foreign key (task_id) references task (id) on delete restrict,
    test_id                 uuid        not null,
    foreign key (test_id) references test (id) on delete restrict,
    topic_id                int         not null,
    foreign key (topic_id) references topic (id) on delete restrict,
    is_correct              boolean     not null default false,
    count_correct_answers   integer     not null default 0 check ( count_correct_answers >= 0 ),
    count_incorrect_answers integer     not null default 0 check ( count_incorrect_answers >= 0 ),
    user_answer             text null,
    time_to_solve           integer     not null check ( time_to_solve > 0 ),
    link_to_code            varchar(250) null unique,
    extra_text              text null,
    updated_at              timestamptz not null default now(),
    created_at              timestamptz not null default now(),
    unique (user_id, task_id, test_id)
);

COMMENT
ON TABLE user_room_test_task IS
    'Итоговый результат ученика по одной задаче в тесте. '
    'Запись создаётся при завершении теста или при первой попытке (уточняется приложением). '
    'Является источником данных для: '
    '1) sync_user_topic_stat() — статистика по темам; '
    '2) sync_test_task_stat() — статистика задачи в тесте (дашборд учителя); '
    '3) cron-job recalculate_task_stats — avg_solve_time в task. '
    'time_to_solve вычисляется из user_task_event при создании записи. '
    'topic_id денормализован из task — исключает JOIN при агрегации по темам. '
    'ON DELETE RESTRICT на все FK — удаление сущностей требует предварительной очистки результатов.';

COMMENT
ON COLUMN user_room_test_task.id IS 'Суррогатный первичный ключ (serial). Уникальность (user_id, task_id, test_id) — отдельный UNIQUE.';
COMMENT
ON COLUMN user_room_test_task.user_id IS 'FK на пользователя. ON DELETE RESTRICT.';
COMMENT
ON COLUMN user_room_test_task.room_id IS 'FK на комнату. ON DELETE RESTRICT. Денормализован для фильтрации без JOIN к test.';
COMMENT
ON COLUMN user_room_test_task.task_id IS 'FK на задачу. ON DELETE RESTRICT.';
COMMENT
ON COLUMN user_room_test_task.test_id IS 'FK на тест. ON DELETE RESTRICT.';
COMMENT
ON COLUMN user_room_test_task.topic_id IS 'Денормализованный FK на тему задачи (копия task.topic_id). Позволяет sync_user_topic_stat() и cron-job агрегировать данные по теме без JOIN к task.';
COMMENT
ON COLUMN user_room_test_task.is_correct IS 'Итоговый результат: true если задача решена правильно (хотя бы один правильный ответ).';
COMMENT
ON COLUMN user_room_test_task.count_correct_answers IS 'Количество правильных ответов, поданных учеником за время теста.';
COMMENT
ON COLUMN user_room_test_task.count_incorrect_answers IS 'Количество неправильных ответов, поданных учеником за время теста. Сумма count_correct_answers + count_incorrect_answers ограничена max_attempts теста триггером trg_check_answer_count (кроме is_homework = true).';
COMMENT
ON COLUMN user_room_test_task.user_answer IS 'Последний ответ ученика (текст). Хранится для проверки учителем.';
COMMENT
ON COLUMN user_room_test_task.time_to_solve IS 'Время, затраченное на задачу в секундах (> 0). Вычисляется из user_task_event как разница между последним answer_submitted и первым task_opened. Ответ менее чем за 1 секунду отклоняется как невалидный (CHECK > 0). Используется для расчёта avg_solve_time в task и avg_time в user_topic_stat/test_task_stat.';
COMMENT
ON COLUMN user_room_test_task.link_to_code IS 'S3-путь к коду, сданному учеником по задаче: code/{room_id}/{task_id}/{user_id}.py. NULL если ученик не сдавал код. Уникален глобально — исключает коллизии путей в S3.';
COMMENT
ON COLUMN user_room_test_task.extra_text IS 'Комментарий ученика к ответу (сообщение учителю). NULL если не оставлен.';
COMMENT
ON COLUMN user_room_test_task.updated_at IS 'Время последнего изменения результата. Обновляется триггером user_room_task_updated_at. Используется cron-job recalculate_task_stats для инкрементальной выборки.';
COMMENT
ON COLUMN user_room_test_task.created_at IS 'Время создания записи о результате.';

create index user_room_task_task_id_index on user_room_test_task (task_id);
COMMENT
ON INDEX user_room_task_task_id_index IS 'Btree-индекс по task_id. Ускоряет выборку всех результатов по задаче — используется в sync_test_task_stat() и cron-job recalculate_task_stats.';

create index user_room_task_updated_at_index on user_room_test_task (updated_at desc);
COMMENT
ON INDEX user_room_task_updated_at_index IS 'Btree-индекс по updated_at DESC. Используется cron-job recalculate_task_stats для инкрементальной выборки изменённых результатов.';

create trigger user_room_task_updated_at
    before update
    on user_room_test_task
    for each row execute procedure set_updated_at();

COMMENT
ON TRIGGER user_room_task_updated_at ON user_room_test_task IS 'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';

create trigger trg_check_answer_count
    before insert or
update on user_room_test_task
    for each row execute function check_answer_count();

COMMENT
ON TRIGGER trg_check_answer_count ON user_room_test_task IS 'BEFORE INSERT/UPDATE: проверяет, что count_correct_answers + count_incorrect_answers не превышает max_attempts теста (функция check_answer_count()). Пропускается для домашних работ (is_homework = true). Обеспечивает честность ограничения попыток на уровне БД.';

create trigger trg_sync_user_topic_stat
    after insert or
update on user_room_test_task
    for each row execute function sync_user_topic_stat();

COMMENT
ON TRIGGER trg_sync_user_topic_stat ON user_room_test_task IS 'После каждого INSERT/UPDATE вызывает sync_user_topic_stat() для пересчёта статистики пользователя по теме в user_topic_stat. Обеспечивает реалтайм-консистентность статистики при решении задач в тестах.';

create trigger trg_sync_test_task_stat
    after insert or
update on user_room_test_task
    for each row execute procedure sync_test_task_stat();

COMMENT
ON TRIGGER trg_sync_test_task_stat ON user_room_test_task IS 'После каждого INSERT/UPDATE вызывает sync_test_task_stat() для пересчёта агрегированной статистики задачи в тесте (test_task_stat). Источник данных для дашборда учителя — обновляется реалтайм.';