create table user_task_event
(
    id          bigserial,
    user_id     uuid        not null references "user" (id) on delete restrict,
    room_id     uuid not null references room (id) on delete restrict,
    task_id     int         not null references task (id) on delete restrict,
    test_id     uuid        not null references test (id) on delete restrict,
    event       event_type  not null,
    occurred_at timestamptz not null default now(),
    primary key (id, occurred_at)
) partition by range (occurred_at);

COMMENT ON TABLE user_task_event IS
    'Event-лог действий ученика при работе над задачей внутри теста. '
    'Фиксирует все ключевые события: открытие задачи, отправку ответов, работу с IDE, '
    'переключение вкладок. Используется для: '
    '1) расчёта time_to_solve в user_room_test_task при завершении теста; '
    '2) построения диаграмм активности для дашборда учителя; '
    '3) реконструкции хронологии работы ученика. '
    'Только тестовый контекст — события при работе с банком задач не логируются. '
    'Партиционирована по месяцам (occurred_at) через pg_partman: '
    'партиции создаются автоматически (3 вперёд), партиции старше 12 месяцев дропаются. '
    'PRIMARY KEY включает occurred_at — обязательное требование range-партиционирования. '
    'ON DELETE RESTRICT на все FK — удаление пользователя/комнаты/задачи/теста '
    'требует предварительной очистки событий (защита от потери аналитики).';

COMMENT ON COLUMN user_task_event.id IS 'Bigserial — суррогатный идентификатор события. Вместе с occurred_at образует PRIMARY KEY.';
COMMENT ON COLUMN user_task_event.user_id IS 'FK на пользователя. ON DELETE RESTRICT — нельзя удалить пользователя, имеющего события.';
COMMENT ON COLUMN user_task_event.room_id IS 'FK на комнату. ON DELETE RESTRICT.';
COMMENT ON COLUMN user_task_event.task_id IS 'FK на задачу. ON DELETE RESTRICT.';
COMMENT ON COLUMN user_task_event.test_id IS 'FK на тест. ON DELETE RESTRICT.';
COMMENT ON COLUMN user_task_event.event IS 'Тип события (event_type): task_opened, answer_submitted, code_run, open_ide, insert_in_ide, come_back_to_page, go_out_from_page.';
COMMENT ON COLUMN user_task_event.occurred_at IS 'Время события (UTC). Является ключом партиционирования — входит в PRIMARY KEY. Партиции: 1 месяц = 1 партиция. Хранятся последние 12 месяцев.';

create index on user_task_event (user_id, test_id, task_id, occurred_at);

select partman.create_parent(
               p_parent_table := 'public.user_task_event',
               p_control := 'occurred_at',
               p_interval := '1 month',
               p_premake := 3
       );

update partman.part_config
set retention            = '12 months',
    retention_keep_table = false
where parent_table = 'public.user_task_event';