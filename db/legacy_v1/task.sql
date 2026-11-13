create table task
(
    id                 serial primary key,
    text               text          not null,
    embedding          vector(1536)  not null,
    correct_answer     text[]        not null,
    link_to_image      varchar(100)  null unique,
    embedding_of_image vector(1536)  null,
    link_to_files      text[]        null,
    difficulty         int           not null check ( difficulty > 0 and difficulty <= 3 ),
    subject_id         int           not null,
    foreign key (subject_id) references subject (id) on delete restrict,
    topic_id           int           not null,
    foreign key (topic_id, subject_id) references topic (id, subject_id),
    avg_solve_time     integer       null check ( avg_solve_time >= 0 ),
    solve_rate         numeric(5, 2) null check ( solve_rate >= 0 and solve_rate <= 100 ),
    avg_attempts       numeric(5, 2) null check ( avg_attempts >= 0 ),
    attempts_histogram int[15]       not null default array_fill(0, array [15]) check (array_length(attempts_histogram, 1) = 15),
    bank_stats_count   integer       not null default 0 check ( bank_stats_count >= 0 ),
    test_stats_count   integer       not null default 0 check ( test_stats_count >= 0 ),
    updated_at         timestamptz   not null default now(),
    created_at         timestamptz   not null default now()
);

COMMENT ON TABLE task IS
    'Банк задач ЕГЭ. Нижний уровень иерархии контента: subject → topic → task. '
    'Хранит текст задачи, медиавложения, правильные ответы, семантические эмбеддинги '
    'и денормализованные агрегаты (пересчитываются cron-job-ом). '
    'correct_answer — массив text[], поддерживает задачи с одним ответом, '
    'несколькими ответами и задачи с под-вопросами. Логика сравнения ответов — на стороне приложения. '
    'Денормализация subject_id и topic_id в связанных таблицах (user_task, user_room_test_task) '
    'позволяет избегать JOIN к task при агрегации статистики по темам.';

COMMENT ON COLUMN task.id IS 'Суррогатный первичный ключ задачи (serial).';
COMMENT ON COLUMN task.text IS 'Полный текст задачи. Индексируется GIN-триграммами (task_text_index) для поиска подстрок и нечёткого совпадения.';
COMMENT ON COLUMN task.embedding IS 'Векторное представление текста задачи размером 1536 (формат зависит от embedding-модели). Используется для семантического поиска похожих задач через HNSW-индекс (cosine distance). При запросе рекомендуется: SET LOCAL hnsw.ef_search = 64.';
COMMENT ON COLUMN task.correct_answer IS 'Массив правильных ответов. Поддерживает: одиночный ответ (массив из 1 элемента), несколько допустимых ответов, ответы на несколько под-вопросов. Сравнение с ответом пользователя выполняется приложением, не БД.';
COMMENT ON COLUMN task.link_to_image IS 'S3-путь к изображению задачи. NULL если изображения нет. Уникален глобально — исключает коллизии путей в S3 на уровне БД.';
COMMENT ON COLUMN task.embedding_of_image IS 'Векторное представление изображения задачи (1536 dim). NULL если изображения нет. HNSW-индекс на это поле не создаётся — используется только текстовый embedding.';
COMMENT ON COLUMN task.link_to_files IS 'Массив S3-путей к прикреплённым файлам (условия, данные и т.п.). NULL если файлов нет.';
COMMENT ON COLUMN task.difficulty IS 'Уровень сложности задачи: 1 — лёгкая, 2 — средняя, 3 — сложная. Используется рекомендательной системой (adaptive_difficulty) и фильтрацией задач.';
COMMENT ON COLUMN task.subject_id IS 'FK на предмет (subject). Денормализована для дешёвой фильтрации без JOIN к topic. ON DELETE RESTRICT — удаление предмета запрещено при наличии задач.';
COMMENT ON COLUMN task.topic_id IS 'FK на тему (topic). Ссылается на составной ключ (id, subject_id) таблицы topic — гарантирует консистентность: задача принадлежит теме именно своего предмета.';
COMMENT ON COLUMN task.avg_solve_time IS 'Среднее время решения задачи в секундах. Источник: user_room_test_task.time_to_solve. Пересчитывается cron-job recalculate_task_stats (03:00). NULL до первого пересчёта. Учитывает только решения в тестах — банк задач время не фиксирует.';
COMMENT ON COLUMN task.solve_rate IS 'Процент пользователей, у которых is_correct = true в user_task (0–100). Пересчитывается cron-job recalculate_task_stats (03:00). NULL до первого пересчёта.';
COMMENT ON COLUMN task.avg_attempts IS 'Среднее количество попыток на задачу (total_attempts из user_task). Пересчитывается cron-job recalculate_task_stats (03:00). NULL до первого пересчёта.';
COMMENT ON COLUMN task.attempts_histogram IS 'Массив ровно 15 элементов. Гистограмма распределения усилий при решении задачи. Индекс [i] (i = 1..14) — количество пользователей, решивших задачу ровно за i попыток в рамках текущей сессии (attempts_since_last_solve, не total_attempts). Индекс [15] — количество пользователей, потративших 15 и более попыток. Обновляется атомарным UPDATE в FastAPI BackgroundTask при успешном решении, не чаще одного раза в 7 дней на пользователя (histogram_updated_at в user_task). Атомарность: UPDATE user_task SET histogram_updated_at = now() WHERE ... AND (histogram_updated_at IS NULL OR histogram_updated_at < now() - interval ''7 days'') RETURNING attempts_since_last_solve — только воркер, получивший RETURNING, инкрементирует гистограмму. Используется для расчёта перцентиля пользователя без обращения к сырым данным user_task.';
COMMENT ON COLUMN task.bank_stats_count IS 'Приближённое количество уникальных пользователей, решавших задачу в банке задач (user_task). Считается через HLL (hll_cardinality) в cron-job для дешёвого COUNT DISTINCT. Небольшая погрешность (~1–2%) допустима. Обновляется cron-job recalculate_task_stats.';
COMMENT ON COLUMN task.test_stats_count IS 'Приближённое количество уникальных пользователей, решавших задачу в тестах (user_room_test_task). Считается через HLL в cron-job. Обновляется cron-job recalculate_task_stats.';
COMMENT ON COLUMN task.updated_at IS 'Время последнего изменения задачи. Обновляется триггером task_updated_at. Используется cron-job recalculate_task_stats для инкрементальной выборки изменённых задач.';
COMMENT ON COLUMN task.created_at IS 'Время добавления задачи в банк.';

create index task_text_index on task using gin (lower(text) gin_trgm_ops);
COMMENT ON INDEX task_text_index IS 'GIN-индекс по trigram-хешам lower(text). Обеспечивает быстрый поиск задач по подстроке и нечёткому совпадению (LIKE, ILIKE, similarity). Требует расширения pg_trgm.';

create index task_subject_id_index on task (subject_id);
COMMENT ON INDEX task_subject_id_index IS 'Btree-индекс по subject_id. Ускоряет выборку задач конкретного предмета.';

create index task_topic_id_index on task (topic_id);
COMMENT ON INDEX task_topic_id_index IS 'Btree-индекс по topic_id. Ускоряет выборку задач конкретной темы.';

create index task_difficulty_index on task (difficulty);
COMMENT ON INDEX task_difficulty_index IS 'Btree-индекс по difficulty. Ускоряет фильтрацию задач по уровню сложности (используется в adaptive_difficulty-рекомендациях).';

create index task_created_at_index on task (created_at desc);
COMMENT ON INDEX task_created_at_index IS 'Btree-индекс по created_at DESC. Ускоряет выборку новых задач для модератора.';

create index task_embedding_idx on task using hnsw (embedding vector_cosine_ops) with (m = 16, ef_construction = 128);
COMMENT ON INDEX task_embedding_idx IS 'HNSW-индекс по векторному эмбеддингу текста задачи (cosine distance, pgvector). m=16, ef_construction=128 — баланс между точностью и скоростью построения индекса. При поиске устанавливать: SET LOCAL hnsw.ef_search = 64. Используется рекомендательной системой (similar_task) и семантическим поиском.';

create trigger task_updated_at
    before update on task
    for each row execute procedure set_updated_at();

COMMENT ON TRIGGER task_updated_at ON task IS 'Автоматически обновляет updated_at = now() перед каждым UPDATE через set_updated_at().';