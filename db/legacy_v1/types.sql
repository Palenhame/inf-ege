create type user_role as enum ('superuser', 'moderator', 'user');

COMMENT ON TYPE user_role IS
    'Роль пользователя в системе. '
    'superuser — полный доступ, управление пользователями и контентом. '
    'moderator — управление задачами и контентом, без административных функций. '
    'user      — обычный ученик: решает задачи, участвует в тестах и комнатах.';

create type recommendation_reason as enum (
    'weak_topic',
    'similar_task',
    'adaptive_difficulty'
);

COMMENT ON TYPE recommendation_reason IS
    'Причина выдачи рекомендации задачи пользователю. Используется в task_recommendation_log '
    'для оценки качества каждой стратегии рекомендательного алгоритма. '
    'weak_topic          — задача выбрана по теме с низким score в user_topic_stat. '
    'similar_task        — задача семантически близка к уже решённым (HNSW cosine search). '
    'adaptive_difficulty — задача подобрана по уровню сложности relative to текущего прогресса.';

create type event_type as enum (
    'task_opened',
    'answer_submitted',
    'code_run',
    'open_ide',
    'insert_in_ide',
    'come_back_to_page',
    'go_out_from_page'
);

COMMENT ON TYPE event_type IS
    'Тип события ученика при работе над задачей внутри теста (user_task_event). '
    'task_opened      — ученик открыл или переключился на задачу. '
    'answer_submitted — ученик отправил ответ (правильный или нет). '
    'code_run         — ученик запустил код на выполнение в IDE. '
    'open_ide         — ученик открыл встроенный редактор кода. '
    'insert_in_ide    — ученик вставил текст в редактор кода. '
    'come_back_to_page — ученик вернулся на вкладку с задачей (фокус восстановлен). '
    'go_out_from_page  — ученик покинул вкладку с задачей (потеря фокуса). '
    'Последние два события используются для детектирования списывания и расчёта реального времени решения.';