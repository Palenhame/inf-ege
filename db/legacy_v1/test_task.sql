create table test_task
(
    id      serial primary key,
    task_id int  not null,
    foreign key (task_id) references task (id) on delete cascade,
    test_id uuid not null,
    foreign key (test_id) references test (id) on delete cascade,
    unique (task_id, test_id)
);

COMMENT
ON TABLE test_task IS
    'Связь задач с тестом (many-to-many: task ↔ test). '
    'Одна задача не может быть добавлена в один тест дважды (UNIQUE task_id + test_id). '
    'При удалении задачи или теста запись удаляется каскадно. '
    'Порядок задач в тесте и другие настройки управляются приложением.';

COMMENT
ON COLUMN test_task.id IS 'Суррогатный первичный ключ (serial). Уникальность пары (task_id, test_id) обеспечивается UNIQUE.';
COMMENT
ON COLUMN test_task.task_id IS 'FK на задачу. При удалении задачи — каскадное удаление.';
COMMENT
ON COLUMN test_task.test_id IS 'FK на тест. При удалении теста — каскадное удаление.';

create index test_task_test_id_task_id_index on test_task (test_id, task_id);
COMMENT
ON INDEX test_task_test_id_task_id_index IS 'Составной btree-индекс (test_id, task_id). Ведущая колонка — test_id: покрывает основной запрос выборки всех задач конкретного теста. task_id в конце позволяет выполнять index-only scan без обращения к heap.';