create table test
(
    id                          uuid primary key      default gen_random_uuid(),
    room_id                     uuid         not null,
    foreign key (room_id) references room (id) on delete cascade,
    name                        varchar(250) not null,
    max_attempts                int          not null default 4 check ( max_attempts >= 4 and max_attempts <= 10 ),
    date_of_start               timestamptz  not null,
    date_of_deadline            timestamptz  not null,
    is_can_solve_after_deadline bool         not null default false,
    is_homework                 bool         not null default false,
    updated_at                  timestamptz  not null default now(),
    created_at                  timestamptz  not null default now(),
    check (date_of_start < date_of_deadline)
);

COMMENT ON TABLE test IS
    'Тест внутри комнаты — временной набор задач с дедлайном. '
    'Ученики сдают задачи теста в ограниченном количестве попыток (max_attempts). '
    'Для домашних работ (is_homework = true) лимит попыток не применяется. '
    'is_can_solve_after_deadline = true разрешает сдачу задач после дедлайна. '
    'При удалении комнаты все тесты удаляются каскадно.';

COMMENT ON COLUMN test.id IS 'UUID теста, генерируется автоматически.';
COMMENT ON COLUMN test.room_id IS 'FK на комнату. При удалении комнаты тест удаляется каскадно.';
COMMENT ON COLUMN test.name IS 'Название теста. Уникальность в рамках комнаты не ограничена — контролируется приложением.';
COMMENT ON COLUMN test.max_attempts IS 'Максимальное количество попыток ответа на одну задачу теста (4–10, по умолчанию 4). Проверяется триггером trg_check_answer_count в user_room_test_task. Не применяется если is_homework = true.';
COMMENT ON COLUMN test.date_of_start IS 'Дата и время начала теста. Ученики не могут сдавать задачи до этого момента. Гарантированно меньше date_of_deadline (CHECK).';
COMMENT ON COLUMN test.date_of_deadline IS 'Дата и время дедлайна теста. После этого момента сдача возможна только если is_can_solve_after_deadline = true.';
COMMENT ON COLUMN test.is_can_solve_after_deadline IS 'Разрешить сдачу задач после дедлайна. false (по умолчанию) — приложение блокирует новые ответы после date_of_deadline.';
COMMENT ON COLUMN test.is_homework IS 'Признак домашней работы. Если true — лимит max_attempts не применяется (триггер trg_check_answer_count пропускает проверку).';
COMMENT ON COLUMN test.updated_at IS 'Время последнего изменения теста. Обновляется триггером test_updated_at.';
COMMENT ON COLUMN test.created_at IS 'Время создания теста.';

create index test_room_id_index on test (room_id);
COMMENT ON INDEX test_room_id_index IS 'Btree-индекс по room_id. Ускоряет выборку всех тестов конкретной комнаты.';

create index test_updated_at_deadline_index on test (date_of_deadline asc, updated_at desc);
COMMENT ON INDEX test_updated_at_deadline_index IS 'Составной btree-индекс (date_of_deadline ASC, updated_at DESC). Ускоряет выборку активных тестов (дедлайн в будущем) и просроченных в порядке актуальности.';

create trigger test_updated_at
    before update on test
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER test_updated_at ON test IS 'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';