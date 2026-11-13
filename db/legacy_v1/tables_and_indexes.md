# Таблицы и индексы

## Иерархия контента

```
subject → topic → task
```

Пользовательская активность идёт в двух контекстах:
- **Банк задач** — пользователь решает задачи самостоятельно → `user_task`
- **Тест в комнате** — учитель создаёт тест, ученики сдают → `user_room_test_task`

Оба контекста агрегируются в `user_topic_stat`, которая питает рекомендательную систему.

---

## Таблицы

### `user`
Аккаунты пользователей. Аутентификация через Яндекс OAuth или VK OAuth — `yandex_id` / `vk_id` хранят внешние идентификаторы провайдера, `null` если провайдер не привязан. Один пользователь может привязать оба провайдера одновременно.

Роль `user_role` определяет права: `superuser` — полный доступ, `moderator` — управление контентом, `user` — обычный ученик.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | uuid PK | Генерируется автоматически, используется как FK везде |
| `username` | varchar(250) unique | Отображаемое имя, уникально глобально |
| `yandex_id` | varchar(250) unique | ID Яндекс OAuth, null если не привязан |
| `vk_id` | varchar(250) unique | ID VK OAuth, null если не привязан |
| `role` | user_role | superuser / moderator / user, default user |
| `updated_at` | timestamptz | Обновляется триггером |
| `created_at` | timestamptz | Время регистрации |

---

### `subject`
Учебные предметы ЕГЭ — верхний уровень иерархии контента. Примеры: Информатика, Математика, Физика. Название уникально глобально. При удалении предмета все его темы удаляются каскадно, но удаление заблокировано пока есть задачи (`ON DELETE RESTRICT` в `task`).

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | serial PK | Суррогатный ключ |
| `name` | varchar(250) unique | Название предмета, уникально глобально |
| `updated_at` | timestamptz | Обновляется триггером |
| `created_at` | timestamptz | — |

---

### `topic`
Темы внутри предмета — средний уровень иерархии. Примеры для Информатики: Графы, Рекурсия, Системы счисления. Тема принадлежит ровно одному предмету через `subject_id`.

Важная деталь: составной уникальный ключ `(id, subject_id)` создан специально как цель для FK из таблицы `task` — это гарантирует, что задача принадлежит теме именно своего предмета, а не любой теме с тем же `topic_id`.

`topic_id` является осью агрегации в `user_topic_stat` — весь прогресс пользователя по теме собирается именно здесь.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | serial PK | Суррогатный ключ |
| `name` | varchar(250) unique | Название темы, уникально глобально |
| `type` | varchar(10) | Классификатор темы, например "theory" / "practice" |
| `subject_id` | int FK → subject | Предмет темы, cascade delete |
| `updated_at` | timestamptz | Обновляется триггером |
| `created_at` | timestamptz | — |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `topic_name_index` | `name` | btree | Поиск тем по названию в автодополнении без фильтра по предмету |

---

### `task`
Банк задач ЕГЭ — нижний уровень иерархии контента. Хранит всё что нужно для отображения задачи: текст, медиавложения, правильные ответы, векторные эмбеддинги для семантического поиска.

Денормализованные агрегаты (`solve_rate`, `avg_attempts`, `avg_solve_time`, `bank_stats_count`, `test_stats_count`) пересчитываются ежедневным cron-job-ом `recalculate_task_stats` в 03:00, до первого пересчёта — `null`.

`attempts_histogram` — массив ровно 15 элементов. Индекс `[i]` показывает сколько пользователей решили задачу ровно за `i` попыток в рамках одной сессии (индекс 15 = «15 и более»). Обновляется атомарно в FastAPI BackgroundTask, не чаще раза в 7 дней на пользователя — защита от race condition между воркерами.

FK на `topic` ссылается на составной ключ `(id, subject_id)` — гарантирует, что задача принадлежит теме именно своего предмета.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | serial PK | — |
| `text` | text | Полный текст задачи |
| `embedding` | vector(1536) | Эмбеддинг текста для HNSW-поиска |
| `correct_answer` | text[] | Массив правильных ответов, сравнение на стороне приложения |
| `link_to_image` | varchar(100) unique | S3-путь к изображению, null если нет |
| `embedding_of_image` | vector(1536) | Эмбеддинг изображения, HNSW-индекс не создаётся |
| `link_to_files` | text[] | Массив S3-путей к файлам, null если нет |
| `difficulty` | int | Сложность: 1 — лёгкая, 2 — средняя, 3 — сложная |
| `subject_id` | int FK → subject | Денормализован для фильтрации без JOIN к topic |
| `topic_id` | int FK → topic(id, subject_id) | Тема задачи |
| `avg_solve_time` | integer | Среднее время решения в сек., источник: user_room_test_task |
| `solve_rate` | numeric(5,2) | % пользователей решивших задачу, источник: user_task |
| `avg_attempts` | numeric(5,2) | Среднее кол-во попыток, источник: user_task |
| `attempts_histogram` | int[15] | Гистограмма попыток, обновляется атомарно в BackgroundTask |
| `bank_stats_count` | integer | Приближённое число уникальных пользователей (HLL) из user_task |
| `test_stats_count` | integer | Приближённое число уникальных пользователей (HLL) из user_room_test_task |
| `updated_at` | timestamptz | Обновляется триггером, используется cron для инкрементальной выборки |
| `created_at` | timestamptz | — |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `task_text_index` | `lower(text)` | GIN trigram | Поиск задач по подстроке (LIKE, ILIKE, similarity), требует pg_trgm |
| `task_subject_id_index` | `subject_id` | btree | Выборка задач конкретного предмета |
| `task_topic_id_index` | `topic_id` | btree | Выборка задач конкретной темы |
| `task_difficulty_index` | `difficulty` | btree | Фильтрация по сложности в adaptive_difficulty-рекомендациях |
| `task_created_at_index` | `created_at DESC` | btree | Выборка новых задач для модератора |
| `task_embedding_idx` | `embedding` | HNSW cosine | Семантический поиск похожих задач, m=16 ef_construction=128, при запросе: `SET LOCAL hnsw.ef_search = 64` |

---

### `user_task`
Агрегированное состояние решения задачи из банка конкретным пользователем. Одна запись на пару `(user_id, task_id)` — создаётся при первой попытке, обновляется при каждой следующей.

Является основным источником для расчёта `solve_rate` и `avg_attempts` в `task` (cron-job). Также является источником для `sync_user_topic_stat()` при работе с банком задач.

`topic_id` денормализован из `task` — позволяет триггеру `sync_user_topic_stat()` агрегировать по теме без JOIN к `task`.

`attempts_since_last_solve` — счётчик попыток текущей сессии, сбрасывается при успехе. Именно это значение (а не `total_attempts`) записывается в `task.attempts_histogram`.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | serial PK | Уникальность (user_id, task_id) — отдельный UNIQUE |
| `user_id` | uuid FK → user | Cascade delete |
| `task_id` | int FK → task | Cascade delete |
| `topic_id` | int FK → topic | Денормализация из task, cascade delete |
| `is_correct` | boolean | true если решена правильно хотя бы один раз за всё время |
| `total_attempts` | int | Суммарное кол-во попыток за всё время |
| `attempts_since_last_solve` | int | Попыток с момента последнего успешного решения, сбрасывается в 0 при успехе |
| `histogram_updated_at` | timestamptz | Последнее обновление гистограммы, повторное — не ранее чем через 7 дней |
| `user_answer` | text | Последний ответ пользователя |
| `first_solved_at` | timestamptz | Первое успешное решение, null если не решена |
| `last_solved_at` | timestamptz | Последнее успешное решение, null если не решена |
| `updated_at` | timestamptz | Обновляется триггером, используется cron для инкрементальной выборки |
| `created_at` | timestamptz | Время первой попытки |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `user_task_updated_at_index` | `updated_at DESC` | btree | cron-job `recalculate_task_stats` — инкрементальная выборка задач с активностью после `last_run` |

> UNIQUE `(user_id, task_id)` покрывает запросы по `user_id` — отдельный индекс на `user_id` не нужен.

---

### `user_topic_stat`
Денормализованная агрегированная статистика пользователя по теме. Одна запись на пару `(user_id, topic_id)`. Центральный источник данных для рекомендательной системы.

`solved` / `attempted` / `success_rate` / `avg_time` обновляются реалтайм триггером `sync_user_topic_stat()` после каждого изменения в `user_task` или `user_room_test_task`. `score` пересчитывается ежедневным cron-job-ом `recalculate_topic_score` в 03:30.

`stats_updated_at` — маркер для cron-job-а: пересчитывается `score` только для строк с `stats_updated_at >= last_run`.

`score` — балл слабости темы (0–100). Формула: `(100 - success_rate) * ln(attempted + 1) / normalizer * 100`. Чем ниже точность и чем больше попыток — тем выше score. Строки с `attempted = 0` получают `score = 0` (cold start, рекомендательная система обрабатывает отдельно).

| Поле | Тип | Описание |
|------|-----|----------|
| `user_id` | uuid PK, FK → user | Часть составного PK |
| `topic_id` | int PK, FK → topic | Часть составного PK |
| `solved` | int | Уникальные задачи темы решённые правильно хотя бы раз |
| `attempted` | int | Уникальные задачи темы, которые пользователь пытался решить |
| `success_rate` | numeric(5,2) | solved / attempted * 100 (0–100) |
| `avg_time` | integer | Среднее время работы над задачей темы в сек., источник: user_room_test_task |
| `score` | numeric(5,2) | Балл слабости темы (0–100), пересчитывается cron-job ежедневно |
| `updated_at` | timestamptz | Обновляется триггером |
| `stats_updated_at` | timestamptz | Обновляется в `sync_user_topic_stat()`, маркер для cron-job |
| `created_at` | timestamptz | — |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `user_topic_stat_score_index` | `score` | btree | Выборка слабых тем пользователя для рекомендаций (`ORDER BY score DESC`) |

---

### `task_recommendation_log`
Лог выданных рекомендаций. Запись создаётся в момент выдачи рекомендации. `was_solved` обновляется асинхронно после взаимодействия пользователя с задачей. Используется для оценки качества каждой стратегии рекомендаций и A/B-тестирования.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | serial PK | — |
| `user_id` | uuid FK → user | Cascade delete |
| `task_id` | int FK → task | Cascade delete |
| `reason_id` | recommendation_reason | Стратегия: weak_topic / similar_task / adaptive_difficulty |
| `was_solved` | boolean | true — решил, false — открыл но не решил, null — проигнорировал |
| `created_at` | timestamptz | Время выдачи рекомендации |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `rec_log_user_id_index` | `user_id` | btree | История рекомендаций конкретного пользователя |
| `rec_log_created_at_index` | `created_at DESC` | btree | Выборка последних рекомендаций и очистка устаревших |

---

### `room`
Учебная комната — пространство учителя для проведения тестов. Учитель (`creator_id`) создаёт комнату, приглашает учеников (`user_room`), внутри создаёт тесты (`test`).

При удалении создателя комната сохраняется (`ON DELETE RESTRICT`) — содержимое не должно теряться при смене учителя. Один учитель не может создать две комнаты с одинаковым именем (UNIQUE `creator_id + name`).

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | uuid PK | Генерируется автоматически |
| `name` | varchar(250) | Название комнаты, уникально в рамках одного creator_id |
| `creator_id` | uuid FK → user | ON DELETE RESTRICT |
| `max_members` | integer | Максимум участников: 1–40, default 20 |
| `updated_at` | timestamptz | Обновляется триггером |
| `created_at` | timestamptz | — |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `room_name_index` | `name` | btree | Поиск комнат по названию без фильтра по создателю |
| `room_updated_at_index` | `updated_at` | btree | Сортировка комнат по активности |

---

### `user_room`
Членство пользователя в комнате. Одна запись на пару `(user_id, room_id)`. При удалении пользователя или комнаты запись удаляется каскадно.

`link_to_code` — S3-путь к коду из свободного редактора комнаты (`code/{room_id}/{user_id}.py`). Отдельное поле от `user_room_test_task.link_to_code` — это код из свободного редактора, не сданный в тесте.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | serial PK | Уникальность (user_id, room_id) — отдельный UNIQUE |
| `user_id` | uuid FK → user | Cascade delete |
| `room_id` | uuid FK → room | Cascade delete |
| `link_to_code` | varchar(250) unique | S3-путь к коду свободного редактора, null если не писал |
| `updated_at` | timestamptz | Обновляется триггером |
| `created_at` | timestamptz | Время вступления в комнату |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `user_room_room_id_index` | `room_id` | btree | Выборка всех участников конкретной комнаты |
| `user_room_updated_at_index` | `updated_at DESC` | btree | Последние активные участники |

---

### `test`
Тест внутри комнаты — временной набор задач с дедлайном. Ученики сдают задачи в ограниченном количестве попыток (`max_attempts`: 4–10, default 4). При удалении комнаты тест удаляется каскадно.

`is_homework = true` отключает лимит попыток — триггер `trg_check_answer_count` пропускает проверку. `is_can_solve_after_deadline = true` разрешает сдачу после дедлайна. CHECK гарантирует `date_of_start < date_of_deadline`.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | uuid PK | — |
| `room_id` | uuid FK → room | Cascade delete |
| `name` | varchar(250) | Название теста |
| `max_attempts` | int | Максимум попыток на задачу (4–10), не применяется для домашних работ |
| `date_of_start` | timestamptz | Начало теста, гарантированно < deadline |
| `date_of_deadline` | timestamptz | Дедлайн |
| `is_can_solve_after_deadline` | bool | Разрешить сдачу после дедлайна, default false |
| `is_homework` | bool | Домашняя работа — лимит попыток не применяется, default false |
| `updated_at` | timestamptz | Обновляется триггером |
| `created_at` | timestamptz | — |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `test_room_id_index` | `room_id` | btree | Все тесты конкретной комнаты |
| `test_updated_at_deadline_index` | `date_of_deadline ASC, updated_at DESC` | btree | Выборка активных тестов (дедлайн в будущем) и просроченных по актуальности |

---

### `test_task`
Связь задач с тестом (many-to-many: `task ↔ test`). Одна задача не может быть добавлена в тест дважды (UNIQUE `task_id + test_id`). При удалении задачи или теста запись удаляется каскадно.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | serial PK | Уникальность (task_id, test_id) — отдельный UNIQUE |
| `task_id` | int FK → task | Cascade delete |
| `test_id` | uuid FK → test | Cascade delete |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `test_task_test_id_task_id_index` | `test_id, task_id` | btree | Ведущая колонка `test_id` — выборка всех задач теста. `task_id` в конце позволяет index-only scan |

---

### `user_task_event`
Event-лог действий ученика при работе над задачей внутри теста. Фиксирует все ключевые события: открытие задачи, отправку ответов, работу с IDE, переключение вкладок.

Используется для:
- расчёта `time_to_solve` в `user_room_test_task` при завершении теста
- построения диаграмм активности для дашборда учителя
- детектирования списывания (`go_out_from_page` / `come_back_to_page`)

Только тестовый контекст — события банка задач не логируются.

**Партиционирована по месяцам** через `pg_partman`. Партиции создаются автоматически (3 вперёд), партиции старше 12 месяцев дропаются. `PRIMARY KEY` включает `occurred_at` — обязательное требование range-партиционирования.

Все FK — `ON DELETE RESTRICT`: нельзя удалить пользователя, комнату, задачу или тест, пока есть события. Это защита аналитики от случайного удаления.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | bigserial | Вместе с occurred_at образует PK |
| `user_id` | uuid FK → user | ON DELETE RESTRICT |
| `room_id` | uuid FK → room | ON DELETE RESTRICT |
| `task_id` | int FK → task | ON DELETE RESTRICT |
| `test_id` | uuid FK → test | ON DELETE RESTRICT |
| `event` | event_type | Тип события (см. ниже) |
| `occurred_at` | timestamptz PK | Ключ партиционирования, входит в PK |

**Типы событий (`event_type`):**

| Значение | Описание |
|----------|----------|
| `task_opened` | Ученик открыл или переключился на задачу |
| `answer_submitted` | Ученик отправил ответ |
| `code_run` | Ученик запустил код в IDE |
| `open_ide` | Ученик открыл редактор кода |
| `insert_in_ide` | Ученик вставил текст в редактор |
| `come_back_to_page` | Ученик вернулся на вкладку (фокус восстановлен) |
| `go_out_from_page` | Ученик покинул вкладку (потеря фокуса) |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| *(безымянный)* | `user_id, test_id, task_id, occurred_at` | btree | Основной запрос: события конкретного ученика по задаче в тесте в хронологическом порядке |

---

### `user_room_test_task`
Итоговый результат ученика по одной задаче в тесте. Одна запись на тройку `(user_id, task_id, test_id)`.

Является источником данных для:
- `sync_user_topic_stat()` — статистика пользователя по теме
- `sync_test_task_stat()` — статистика задачи в тесте (дашборд учителя)
- cron-job `recalculate_task_stats` — `avg_solve_time` в `task`

`time_to_solve` вычисляется из `user_task_event` при создании записи. `topic_id` денормализован из `task`. Все FK — `ON DELETE RESTRICT`: результаты ученика защищены от удаления при удалении связанных сущностей.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | serial PK | Уникальность (user_id, task_id, test_id) — отдельный UNIQUE |
| `user_id` | uuid FK → user | ON DELETE RESTRICT |
| `room_id` | uuid FK → room | ON DELETE RESTRICT, денормализован для фильтрации без JOIN к test |
| `task_id` | int FK → task | ON DELETE RESTRICT |
| `test_id` | uuid FK → test | ON DELETE RESTRICT |
| `topic_id` | int FK → topic | ON DELETE RESTRICT, денормализован из task |
| `is_correct` | boolean | Итоговый результат: решена задача или нет |
| `count_correct_answers` | integer | Кол-во правильных ответов за тест |
| `count_incorrect_answers` | integer | Кол-во неправильных ответов, сумма ограничена max_attempts триггером |
| `user_answer` | text | Последний ответ ученика |
| `time_to_solve` | integer | Время в сек. (> 0), вычисляется из user_task_event |
| `link_to_code` | varchar(250) unique | S3-путь к коду: code/{room_id}/{task_id}/{user_id}.py |
| `extra_text` | text | Комментарий ученика учителю |
| `updated_at` | timestamptz | Обновляется триггером |
| `created_at` | timestamptz | — |

**Индексы:**

| Индекс | Поля | Тип | Зачем |
|--------|------|-----|-------|
| `user_room_task_task_id_index` | `task_id` | btree | Все результаты по задаче — используется в `sync_test_task_stat()` и cron |
| `user_room_task_updated_at_index` | `updated_at DESC` | btree | cron `recalculate_task_stats` — инкрементальная выборка |

---

### `test_task_stat`
Агрегированная статистика задачи в конкретном тесте. Одна запись на пару `(test_id, task_id)`. Обновляется реалтайм триггером `trg_sync_test_task_stat` после каждого результата ученика. Источник данных для дашборда учителя — исключает агрегационные запросы к `user_room_test_task` при открытии дашборда.

| Поле | Тип | Описание |
|------|-----|----------|
| `test_id` | uuid PK, FK → test | Cascade delete |
| `task_id` | int PK, FK → task | Cascade delete |
| `avg_time` | integer | Среднее время решения в сек. среди всех учеников теста |
| `solved_count` | int | Кол-во учеников решивших задачу правильно |
| `attempted_count` | int | Кол-во учеников хотя бы раз пытавшихся решить |
| `avg_attempts` | numeric(5,2) | Среднее кол-во попыток среди всех учеников |
| `updated_at` | timestamptz | Обновляется триггером |
| `created_at` | timestamptz | Время первого результата ученика |

---

### `cron_checkpoint`
Вспомогательная таблица для cron-job-ов. Хранит метку времени последнего успешного запуска каждого job-а. Обновляется только при успешном завершении job — при ошибке checkpoint не обновляется и следующий запуск повторит весь диапазон.

| Поле | Тип | Описание |
|------|-----|----------|
| `job_name` | text PK | Имя job-а, совпадает с jobname в cron.job |
| `last_run` | timestamptz | Время последнего успешного завершения, default '1970-01-01' |

---

### `cron_alerts`
Таблица алертов о падении cron-job-ов. Заполняется job-ом `monitor_cron_jobs` каждый час в :05. Содержит только записи со `status = 'failed'` за последние 25 часов без дублей. Внешний мониторинг опрашивает эту таблицу для оповещений.

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | bigserial PK | — |
| `created_at` | timestamptz | Время детектирования (не время падения job-а) |
| `job_name` | text | Имя упавшего job-а |
| `run_id` | bigint | ID запуска из cron.job_run_details, используется для дедупликации |
| `status` | text | Всегда 'failed' |
| `return_msg` | text | Текст ошибки из cron.job_run_details.return_message |
