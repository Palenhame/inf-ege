# Backend.md — Техническая спецификация Бэкенда (Go 1.24+ & PostgreSQL 17)

Данный документ описывает внутреннюю архитектуру бэкенда, обновлённую схему базы данных v2 и пошаговый план реализации всех серверных подсистем. Внешние маршруты и JSON-форматы строго подчиняются `Contract.md`.

---

## 1. Технологический стек Бэкенда

- **Язык**: Go `1.24+`
- **HTTP-роутер**: `github.com/go-chi/chi/v5` (быстрый, совместимый со стандартным `net/http`, удобные группы middleware)
- **PostgreSQL драйвер (Raw SQL)**: `github.com/jackc/pgx/v5` (`pgxpool`, `pgx.Batch` для пакетной вставки событий, нативная работа с `jsonb`, `smallint[]`, `uuid`) — **без ORM!**
- **Миграции БД**: `github.com/pressly/goose/v3` (встраивается через `embed.FS` и автоматически накатывает `db/migrations/*.sql` при старте бинарника или отдельной командой)
- **WebSockets**: `github.com/coder/websocket` (современная неблокирующая реализация с контекстами Go и нулевыми аллокациями в простое)
- **Redis**: `github.com/redis/go-redis/v9` (кэш справочников, sliding-window rate limiter, хранение `refresh` сессий, live-presence учеников)
- **S3 / MinIO**: `github.com/minio/minio-go/v7`
- **JWT & Криптография**: `github.com/golang-jwt/jwt/v5`
- **Логирование**: стандартный структурированный `log/slog` (JSON в prod, цветной текстовый в dev)

---

## 2. База данных PostgreSQL 17 — Ревизия v2 (Что изменено по сравнению со старой `db/`)

### 2.1. Список архитектурных изменений БД
1. **Удалены векторные эмбеддинги**: расширение `vector` (`pgvector`), колонки `task.embedding`, `task.embedding_of_image`, индекс `task_embedding_idx` и значение `'similar_task'` в `recommendation_reason` полностью убраны.
2. **Удалена таблица `subject`**: так как платформа посвящена только КЕГЭ по информатике, убраны таблица `subject`, все колонки `subject_id` и составные внешние ключи `(topic_id, subject_id)`.
3. **Отказ от нестандартных C-расширений Postgres (`pg_cron`, `pg_partman`, `hll`)**:
   - Оставлены только встроенные в стандартный `postgres:17-alpine` расширения `pgcrypto` (или `gen_random_uuid()`) и **`pg_trgm`** (GIN-индекс триграмм для быстрого поиска подстрок в условиях задач).
   - Исправлен старый баг `hll_hash_bigint(uuid)`: точный или инкрементальный пересчёт `solve_rate`, `avg_attempts`, `bank_stats_count` и `score` выполняется горутиной фонового планировщика в Go.
   - Партиционирование `user_task_events` остаётся нативным декларативным `PARTITION BY RANGE (occurred_at)` PostgreSQL 17, а создание будущих партиций `user_task_events_YYYY_MM` раз в сутки проверяет Go-воркер.
4. **Soft Delete (`deleted_at timestamptz`) вместо блокирующих `ON DELETE RESTRICT`**: учителя могут свободно удалять комнаты и тесты, а админы — задачи; данные скрываются фильтром `WHERE deleted_at IS NULL`, сохраняя целостность исторической аналитики.
5. **Специфика КЕГЭ**:
   - `kim_numbers smallint[]` (поддержка как одиночных номеров `{24}`, так и единой карточки `{19,20,21}`).
   - `difficulty` расширен до `1..4` (Базовый, Средний, Сложный, Гроб).
   - Полиморфные `answer_config jsonb`, `correct_answer jsonb`, `user_answer jsonb` и частичные первичные баллы `earned_score` (`0..3`).
   - Таблица шкалы перевода баллов КЕГЭ `exam_score_scale` (`0..29` → `0..100`).
6. **Подписки, Инвайты, Роль `teacher` и Универсальные Работы (`assignments` + `assignment_sessions`)**:
   - Роль `moderator` заменена на `teacher`, `user` переименована в `student`.
   - Добавлены таблицы тарифов, подписок и инвайт-кодов.
   - Таблица `test` эволюционировала в универсальную `assignments` (`room_id` может быть `NULL` для самостоятельных пробников ученика!), а прохождение обёрнуто в `assignment_sessions`.

---

### 2.2. Полная DDL-спецификация миграции `00001_init_schema.sql`

#### 2.2.1. Расширения и Типы (Enums)
```sql
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TYPE user_role AS ENUM ('superuser', 'teacher', 'student');

CREATE TYPE recommendation_reason AS ENUM (
    'weak_topic',
    'adaptive_difficulty'
);

CREATE TYPE assignment_mode AS ENUM (
    'homework',
    'classwork',
    'practice',
    'proctored_test',
    'mock_exam'
);

CREATE TYPE session_status AS ENUM (
    'in_progress',
    'finished',
    'expired'
);

CREATE TYPE event_type AS ENUM (
    'session_started',
    'task_opened',
    'answer_submitted',
    'code_run',
    'open_ide',
    'open_spreadsheet',
    'insert_in_ide',
    'tab_blur',
    'tab_focus',
    'devtools_triggered',
    'session_finished'
);
```

#### 2.2.2. Пользователи, Тарифы, Подписки и Инвайты
```sql
CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      VARCHAR(64)  NOT NULL UNIQUE,
    first_name    VARCHAR(100) NOT NULL DEFAULT '',
    last_name     VARCHAR(100) NOT NULL DEFAULT '',
    is_onboarded  BOOLEAN      NOT NULL DEFAULT FALSE,
    yandex_id     VARCHAR(250) NULL UNIQUE,
    vk_id         VARCHAR(250) NULL UNIQUE,
    role          user_role    NOT NULL DEFAULT 'student',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ  NULL
);

CREATE TABLE subscription_tiers (
    id           SERIAL PRIMARY KEY,
    slug         VARCHAR(64)  NOT NULL UNIQUE,
    name         VARCHAR(150) NOT NULL,
    description  TEXT         NOT NULL DEFAULT '',
    price_rub    INTEGER      NOT NULL DEFAULT 0 CHECK (price_rub >= 0),
    is_public    BOOLEAN      NOT NULL DEFAULT TRUE,
    sort_order   INTEGER      NOT NULL DEFAULT 0,
    features     JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE user_subscriptions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    tier_id     INTEGER     NOT NULL REFERENCES subscription_tiers(id),
    starts_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    granted_by  UUID        NULL REFERENCES users(id),
    note        TEXT        NULL,
    revoked_at  TIMESTAMPTZ NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_user_subscriptions_lookup ON user_subscriptions (user_id, expires_at DESC) WHERE revoked_at IS NULL;

CREATE TABLE subscription_invites (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code          VARCHAR(32) NOT NULL UNIQUE,
    tier_id       INTEGER     NOT NULL REFERENCES subscription_tiers(id),
    duration_days INTEGER     NOT NULL CHECK (duration_days > 0),
    max_uses      INTEGER     NOT NULL DEFAULT 1 CHECK (max_uses > 0),
    used_count    INTEGER     NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    expires_at    TIMESTAMPTZ NULL,
    note          TEXT        NULL,
    created_by    UUID        NOT NULL REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ NULL
);

CREATE TABLE subscription_invite_uses (
    invite_id UUID        NOT NULL REFERENCES subscription_invites(id) ON DELETE CASCADE,
    user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    used_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (invite_id, user_id)
);
```

#### 2.2.3. Темы КИМ, Задачи, Шкала ЕГЭ и Прогресс в Банке
```sql
CREATE TABLE topics (
    id          SERIAL PRIMARY KEY,
    kim_number  SMALLINT     NOT NULL CHECK (kim_number >= 1 AND kim_number <= 27),
    name        VARCHAR(250) NOT NULL,
    sort_order  INTEGER      NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ  NULL,
    UNIQUE (kim_number, name)
);

CREATE TABLE tasks (
    id                 SERIAL PRIMARY KEY,
    owner_id           UUID          NULL REFERENCES users(id), -- NULL = задача общего банка, иначе приватная задача учителя
    is_public_bank     BOOLEAN       NOT NULL DEFAULT TRUE,
    kim_numbers        SMALLINT[]    NOT NULL CHECK (cardinality(kim_numbers) IN (1, 3)),
    topic_id           INTEGER       NOT NULL REFERENCES topics(id),
    difficulty         SMALLINT      NOT NULL CHECK (difficulty BETWEEN 1 AND 4),
    source             VARCHAR(250)  NULL,
    text_md            TEXT          NOT NULL,
    images             TEXT[]        NOT NULL DEFAULT '{}',
    files              JSONB         NOT NULL DEFAULT '[]'::jsonb,
    answer_config      JSONB         NOT NULL,
    correct_answer     JSONB         NOT NULL,
    solution_md        TEXT          NULL,
    solution_code      TEXT          NULL,
    avg_solve_time     INTEGER       NULL CHECK (avg_solve_time >= 0),
    solve_rate         NUMERIC(5, 2) NULL CHECK (solve_rate BETWEEN 0 AND 100),
    avg_attempts       NUMERIC(5, 2) NULL CHECK (avg_attempts >= 0),
    attempts_histogram INT[15]       NOT NULL DEFAULT array_fill(0, ARRAY[15]),
    bank_stats_count   INTEGER       NOT NULL DEFAULT 0,
    test_stats_count   INTEGER       NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ   NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ   NULL
);

CREATE INDEX idx_tasks_text_trgm   ON tasks USING gin (lower(text_md) gin_trgm_ops) WHERE deleted_at IS NULL AND is_public_bank = TRUE;
CREATE INDEX idx_tasks_kim_gin     ON tasks USING gin (kim_numbers) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_topic       ON tasks (topic_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_difficulty  ON tasks (difficulty) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_owner       ON tasks (owner_id) WHERE owner_id IS NOT NULL AND deleted_at IS NULL;

CREATE TABLE exam_score_scale (
    primary_score   SMALLINT PRIMARY KEY CHECK (primary_score BETWEEN 0 AND 29),
    secondary_score SMALLINT NOT NULL CHECK (secondary_score BETWEEN 0 AND 100)
);

CREATE TABLE user_tasks (
    user_id                   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_id                   INTEGER     NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    topic_id                  INTEGER     NOT NULL REFERENCES topics(id),
    is_correct                BOOLEAN     NOT NULL DEFAULT FALSE,
    best_score                SMALLINT    NOT NULL DEFAULT 0,
    total_attempts            INTEGER     NOT NULL DEFAULT 0,
    attempts_since_last_solve INTEGER     NOT NULL DEFAULT 0,
    histogram_updated_at      TIMESTAMPTZ NULL,
    last_user_answer          JSONB       NULL,
    first_solved_at           TIMESTAMPTZ NULL,
    last_solved_at            TIMESTAMPTZ NULL,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, task_id)
);
CREATE INDEX idx_user_tasks_updated_at ON user_tasks (updated_at DESC);

CREATE TABLE user_topic_stats (
    user_id          UUID          NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    topic_id         INTEGER       NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    solved           INTEGER       NOT NULL DEFAULT 0,
    attempted        INTEGER       NOT NULL DEFAULT 0,
    success_rate     NUMERIC(5, 2) NOT NULL DEFAULT 0,
    avg_time         INTEGER       NOT NULL DEFAULT 0,
    score            NUMERIC(5, 2) NOT NULL DEFAULT 0,
    stats_updated_at TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, topic_id)
);
CREATE INDEX idx_user_topic_stats_weak ON user_topic_stats (user_id, score DESC);

CREATE TABLE task_recommendation_logs (
    id         BIGSERIAL PRIMARY KEY,
    user_id    UUID                  NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_id    INTEGER               NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    reason     recommendation_reason NOT NULL,
    was_solved BOOLEAN               NULL,
    created_at TIMESTAMPTZ           NOT NULL DEFAULT now()
);
CREATE INDEX idx_rec_logs_user ON task_recommendation_logs (user_id, created_at DESC);
```

#### 2.2.4. Группы, Работы, Сессии прохождения и Партиции Событий
```sql
CREATE TABLE rooms (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(250) NOT NULL,
    creator_id  UUID         NOT NULL REFERENCES users(id),
    invite_code VARCHAR(16)  NOT NULL UNIQUE,
    max_members INTEGER      NOT NULL DEFAULT 25 CHECK (max_members BETWEEN 1 AND 100),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    deleted_at  TIMESTAMPTZ  NULL
);
CREATE INDEX idx_rooms_creator ON rooms (creator_id, updated_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE user_rooms (
    user_id          UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    room_id          UUID         NOT NULL REFERENCES rooms(id) ON DELETE CASCADE,
    free_code_s3_key VARCHAR(250) NULL,
    joined_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, room_id)
);
CREATE INDEX idx_user_rooms_room ON user_rooms (room_id);

CREATE TABLE assignments (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id          UUID            NULL REFERENCES rooms(id), -- NULL = самостоятельный пробник ученика
    creator_id       UUID            NOT NULL REFERENCES users(id),
    title            VARCHAR(250)    NOT NULL,
    mode             assignment_mode NOT NULL,
    settings         JSONB           NOT NULL DEFAULT '{}'::jsonb,
    results_released BOOLEAN         NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ     NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ     NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ     NULL
);
CREATE INDEX idx_assignments_room ON assignments (room_id, created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE assignment_tasks (
    assignment_id UUID     NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    task_id       INTEGER  NOT NULL REFERENCES tasks(id),
    position      SMALLINT NOT NULL,
    PRIMARY KEY (assignment_id, task_id)
);
CREATE INDEX idx_assignment_tasks_order ON assignment_tasks (assignment_id, position ASC);

CREATE TABLE assignment_sessions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    assignment_id   UUID           NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    user_id         UUID           NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status          session_status NOT NULL DEFAULT 'in_progress',
    started_at      TIMESTAMPTZ    NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ    NULL,
    finished_at     TIMESTAMPTZ    NULL,
    primary_score   SMALLINT       NOT NULL DEFAULT 0,
    secondary_score SMALLINT       NULL,
    blur_count      INTEGER        NOT NULL DEFAULT 0,
    paste_count     INTEGER        NOT NULL DEFAULT 0,
    UNIQUE (assignment_id, user_id)
);
CREATE INDEX idx_sessions_active_user ON assignment_sessions (user_id) WHERE status = 'in_progress';
CREATE INDEX idx_sessions_assignment  ON assignment_sessions (assignment_id);

CREATE TABLE user_assignment_tasks (
    session_id        UUID         NOT NULL REFERENCES assignment_sessions(id) ON DELETE CASCADE,
    assignment_id     UUID         NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    user_id           UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    task_id           INTEGER      NOT NULL REFERENCES tasks(id),
    topic_id          INTEGER      NOT NULL REFERENCES topics(id),
    is_correct        BOOLEAN      NOT NULL DEFAULT FALSE,
    earned_score      SMALLINT     NOT NULL DEFAULT 0,
    attempts_used     INTEGER      NOT NULL DEFAULT 0,
    user_answer       JSONB        NULL,
    time_spent_sec    INTEGER      NOT NULL DEFAULT 0,
    code_s3_key       VARCHAR(250) NULL,
    paste_ranges      JSONB        NOT NULL DEFAULT '[]'::jsonb,
    student_comment   TEXT         NULL,
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, task_id)
);
CREATE INDEX idx_uat_task_updated ON user_assignment_tasks (task_id, updated_at DESC);

CREATE TABLE assignment_task_stats (
    assignment_id   UUID          NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    task_id         INTEGER       NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    avg_time_sec    INTEGER       NOT NULL DEFAULT 0,
    solved_count    INTEGER       NOT NULL DEFAULT 0,
    attempted_count INTEGER       NOT NULL DEFAULT 0,
    avg_attempts    NUMERIC(5, 2) NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (assignment_id, task_id)
);

-- Декларативно партиционированный по месяцам лог событий прокторинга и таймлайна
CREATE TABLE user_task_events (
    id            BIGSERIAL,
    session_id    UUID        NOT NULL,
    assignment_id UUID        NOT NULL,
    user_id       UUID        NOT NULL,
    task_id       INTEGER     NULL,
    event         event_type  NOT NULL,
    payload       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    occurred_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

CREATE INDEX idx_user_task_events_session ON user_task_events (session_id, occurred_at ASC);
```
*(Плюс в миграции переносятся триггеры `set_updated_at()`, `sync_user_topic_stat()` и `sync_assignment_task_stat()`, адаптированные под новые имена таблиц).*

---

## 3. Архитектура Go-приложения (`backend/`)

### 3.1. Дерево пакетов
```text
backend/
├── cmd/
│   ├── api/main.go             # Точка входа: конфиг, миграции, пулы БД/Redis/S3, запуск HTTP+WS и Scheduler
│   └── seed/main.go            # Наполнение шкалы КЕГЭ (0..29 -> 0..100), базовых тарифов, тем 1..27 и демо-задач
├── internal/
│   ├── config/                 # Парсинг .env и валидация конфига
│   ├── platform/
│   │   ├── postgres/           # Инициализация pgxpool, обёртки транзакций TxManager
│   │   ├── redis/              # Клиент go-redis, sliding-window rate limiter, кэш-хелперы
│   │   └── storage/            # Клиент minio-go, автосоздание бакетов egeinf-media / egeinf-private
│   ├── transport/
│   │   ├── http/
│   │   │   ├── router.go       # Сборка всех роутов chi /api/v1
│   │   │   ├── middleware/     # AuthCookie, Impersonation, RequireRole, RequireActiveSub, RateLimit, Recoverer
│   │   │   ├── response/       # Единые JSON-ответы и маппинг доменных ошибок в коды Contract.md
│   │   │   └── handlers/       # Тонкие HTTP-хендлеры (парсинг DTO -> вызов Service -> ответ)
│   │   └── ws/
│   │       ├── hub.go          # Неблокирующий мультиплексор подписок (Assignments Matrix + Live Student Stream)
│   │       ├── client.go       # ReadPump / WritePump горутины одного сокета с буферизацией и ping/pong
│   │       └── batcher.go      # Асинхронный буфер записи пачек телеметрии в user_task_events через pgx.Batch
│   ├── domain/
│   │   ├── auth/               # OAuth Яндекс + VK, генерация/ротация JWT + Redis refresh-сессий
│   │   ├── users/              # Профиль, онбординг ФИ + никнейм, админ-управление
│   │   ├── subscriptions/      # Тарифы, проверка квот, активация инвайт-ссылок, Read-Only статус
│   │   ├── tasks/              # CRUD задач, фильтр активных пробников, проверка ответов (4 полиморфных типа!), CSV/ZIP импортёр
│   │   ├── recommendations/    # Алгоритмы weak_topic + adaptive_difficulty + гистограмма попыток
│   │   ├── rooms/              # Группы учителя, короткие инвайт-коды, свободный редактор кода
│   │   ├── assignments/        # Конструктор работ, генератор самостоятельных пробников 1..27, таймеры, подсчёт баллов 29->100
│   │   └── proctoring/         # Выгрузка инспекции сессии, сборка таймлайна и чтение кодов из S3
│   ├── jobs/                   # Фоновый планировщик (замена pg_cron и pg_partman внутри Go)
│   └── mcp/                    # Чистый интерфейс инструментов учителя под будущий MCP-сервер
└── Dockerfile                  # Multi-stage сборка (golang:1.24-alpine -> alpine:3.21)
```

---

## 4. Детальное проектирование подсистем Бэкенда

### 4.1. Аутентификация, OAuth, Impersonation и Безопасность
#### 4.1.1. OAuth Flow (Яндекс ID и VK ID)
- При вызове `GET /api/v1/auth/oauth/{provider}/login` генерируется криптослучайный `state` (плюс PKCE `code_verifier` для VK ID), сохраняется в короткоживущую `httpOnly` куку (5 мин) вместе с `redirect_to`.
- На `/callback` бэкенд обменивает `code` на профиль у провайдера, ищет пользователя по `yandex_id` / `vk_id`.
- Если пользователь новый:
  - Создаётся запись в `users` с временным `username = "user_" + short_hex`, `is_onboarded = false`.
  - Проверяются переменные `BOOTSTRAP_SUPERUSER_YANDEX_ID` / `BOOTSTRAP_SUPERUSER_VK_ID`: при совпадении сразу выставляется `role = 'superuser'`.
- Выставляются две `httpOnly` куки: `ege_access` (JWT HS256, 15 мин) и `ege_refresh` (`session_jti`, хранится в Redis по ключу `auth:refresh:{user_id}:{jti}` с TTL 30 дней).

#### 4.1.2. Middleware `Impersonation` и `SubscriptionGuard`
- В `AuthMiddleware`: после проверки `ege_access`, если реальная роль пользователя `superuser` И присутствует заголовок `X-Impersonate-User`, контекст запроса переключает `EffectiveUserID` и `EffectiveRole` на целевого пользователя, сохраняя `ImpersonatorID`.
- В `SubscriptionGuard`: при мутирующих запросах учителя (`POST /rooms`, `POST /assignments`, `POST /teacher/tasks`) сервис проверяет наличие активной записи в `user_subscriptions` (`expires_at > now() AND revoked_at IS NULL`). Если подписка истекла — возвращается `403 SUBSCRIPTION_READ_ONLY`, но все `GET`-запросы групп и аналитики проходят беспрепятственно!

---

### 4.2. Движок проверки ответов (`internal/domain/tasks/grader.go`)
Вся проверка ответов сосредоточена в чистой, покрытой unit-тестами функции `GradeAnswer(cfg AnswerConfig, correct CorrectAnswer, user UserAnswer) GradeResult`:
1. **Нормализация строк**: если `!cfg.CaseSensitive` → `strings.ToLower`, если `cfg.TrimWhitespace` → удаление пробелов по краям и приведение десятичной запятой к каноническому виду там, где допустимы оба варианта.
2. **`single`**: `1` балл, если нормализованный ответ входит в `correct.Accepted`.
3. **`pair`**:
   - При `max_score == 1`: `1` балл только при совпадении обоих элементов `[0]` и `[1]`.
   - При `max_score == 2` (№26, №27): считает число совпадений по позициям (`0`, `1` или `2` балла); если `allow_swap_for_1pt == true` и элементы перепутаны местами — начисляется `1` балл.
4. **`table`** (№25):
   - Отрезает пустые хвостовые строки в матрице ученика (`ignore_trailing_empty_rows`).
   - Сравнивает построчно с `correct.Matrix`. Полное совпадение → `max_score`; расхождение не более `partial_1pt_max_errors` строк → `1` балл; иначе `0`.
5. **`triplet_19_21`**:
   - Независимо проверяет `ans_19` (1 балл), `ans_20` (1 балл — с сортировкой или точным порядком чисел), `ans_21` (1 балл). Суммарно возвращает `0..3` балла. `is_correct = (earned_score == 3)`.

---

### 4.3. Фильтрация Банка задач и Защита активных пробников
В SQL-запросе `ListBankTasks`:
- Поиск `q`: если `q` начинается с `#` или состоит только из цифр и задача с таким `id` существует — поднимает точное совпадение `t.id = $q_id` на первое место, иначе ищет `lower(t.text_md) LIKE '%' || lower($q) || '%'` с использованием GIN-индекса `pg_trgm`.
- Фильтр по номеру КИМ (`kim`): `t.kim_numbers @> ARRAY[$kim::smallint]` (использует GIN-индекс массива — при выборе КИМ 19, 20 или 21 находится единая карточка `{19,20,21}`!).
- **Исключение задач активного пробника/теста**: если запрос авторизован (`$user_id IS NOT NULL`), добавляется условие:
  ```sql
  AND NOT EXISTS (
      SELECT 1
      FROM assignment_sessions s
      JOIN assignments a ON a.id = s.assignment_id
      JOIN assignment_tasks at ON at.assignment_id = a.id
      WHERE s.user_id = $user_id
        AND s.status = 'in_progress'
        AND a.mode IN ('mock_exam', 'proctored_test')
        AND at.task_id = t.id
  )
  ```
  Благодаря частичному индексу `idx_sessions_active_user` эта подвыборка занимает `< 0.1 мс`!

---

### 4.4. Массовый импортёр задач (`internal/domain/tasks/importer.go`)
- Принимает `.csv` или `.zip` (распаковка zip в память / временный буфер с защитой от zip-bomb до 150 МБ).
- Парсит CSV построчно с накоплением детального отчёта ошибок (`row_number`, `column`, `error_message`).
- Если `?dry_run=true` — валидирует JSON ответов, наличие упомянутых в `text_md`/`images`/`files` медиафайлов внутри ZIP и возвращает предпросмотр распознанных карточек без записи в S3 и Postgres.
- При реальном импорте:
  1. Параллельным пулом воркеров (до 8 горутин) заливает локальные файлы из `media/*` (или внешние URL) в бакет `egeinf-media`, заменяя относительные пути в `text_md` на `/storage/tasks/images/...` и вычисляя точный `size_bytes` каждого файла задачи.
  2. В одной транзакции Postgres создаёт недостающие `topics` и вставляет пачку `tasks`.

---

### 4.5. Высоконагруженный Двухуровневый WebSocket Hub (`internal/transport/ws/`)
В памяти Go (с синхронизацией через `sync.RWMutex` и неблокирующими кольцевыми буферами на каждого клиента, чтобы медленный интернет одного ученика никогда не блокировал рассылку остальным):
1. **Структуры подписок Hub**:
   - `studentConns map[uuid.UUID]*Client` — активный сокет сессии ученика (`session_id -> Client`).
   - `assignmentWatchers map[uuid.UUID]map[*Client]struct{}` — учителя, открывшие матрицу работы (`assignment_id -> set of teacher Clients`).
   - `liveStudentWatchers map[uuid.UUID]map[*Client]struct{}` — учителя, открывшие детальное live-окно конкретного ученика (`session_id -> set of teacher Clients`).
2. **Жизненный цикл On-Demand Live Stream**:
   - При `teacher.watch_student_live(session_id)`: если `len(liveStudentWatchers[session_id])` переходит из `0` в `1`, Hub мгновенно пушит в `studentConns[session_id]` сообщение `server.live_stream_toggle{enabled: true, debounce_ms: 350}`.
   - Если в Redis/памяти уже лежит свежий кэшированный `lastFrame` этой сессии, он отдаётся учителю в первую же миллисекунду открытия модалки.
   - При закрытии вкладки учителя (`defer client.Close()`) или `teacher.unwatch_student_live`: если `len` падает с `1` в `0`, ученику сразу уходит `server.live_stream_toggle{enabled: false}`.
3. **Пакетная запись событий (`TelemetryBatcher`)**:
   - Все входящие события `student.telemetry_batch` складываются в буферизованный канал Go (`chan EventRow`, размер `16384`).
   - Выделенная горутина раз в `500ms` (или при накоплении `200` записей) сбрасывает их одним `pgx.CopyFrom` / `pgx.Batch` в партиционированную таблицу `user_task_events` и атомарно инкрементирует счётчики `blur_count` / `paste_count` в `assignment_sessions`.
   - Параллельно дельта без ожидания диска сразу летит учителям в `assignmentWatchers[assignment_id]`.

---

### 4.6. Фоновый планировщик в Go (`internal/jobs/scheduler.go`)
Запускается в горутине при старте `cmd/api` (с graceful shutdown по `ctx.Done()`, плюс распределённый лок через Redis `SET NX`, если в будущем поднимут 2 реплики бэкенда):
1. **Каждые 15 секунд (`ExpireSessionsJob`)**: находит `assignment_sessions` со `status = 'in_progress' AND expires_at <= now()`, переводит их в `'expired'`, считает итоговые `primary_score` и `secondary_score` по `exam_score_scale` и уведомляет по WS.
2. **Раз в час (`PartitionMaintenanceJob`)**: проверяет наличие месячных партиций `user_task_events_YYYY_MM` на текущий месяц + 3 месяца вперёд (`CREATE TABLE IF NOT EXISTS ... PARTITION OF user_task_events FOR VALUES FROM (...) TO (...)`), а партиции старше 12 месяцев отсоединяет/удаляет.
3. **Ежедневно в 03:00 (`RecalculateTaskStatsJob`)**: инкрементально пересчитывает `solve_rate`, `avg_attempts`, `avg_solve_time`, `bank_stats_count` (`COUNT(DISTINCT user_id)`), `test_stats_count` для задач, по которым была активность за последние 25 часов.
4. **Ежедневно в 03:30 (`RecalculateTopicWeaknessScoreJob`)**: пересчитывает балл слабости тем `score` в `user_topic_stats` по логарифмической формуле `(100 - success_rate) * ln(attempted + 1) / normalizer * 100` для рекомендательной системы.

---

### 4.7. Архитектурный задел под MCP-сервер учителя (`internal/mcp/`)
- Все операции создания авторских задач учителя, загрузки прикреплённых файлов и сборки варианта `assignments` оформляются как чистый фасад `TeacherAutomationFacade` с JSON-Schema описаниями аргументов.
- В таблице пользователей/подписок учтён флаг тарифа `mcp_enabled`, чтобы в следующем релизе подключить персональные API-ключи учителя и транспорт MCP Streamable HTTP без единого изменения доменного кода и БД.

---

## 5. План-декомпозиция работ для Backend-субагентов

1. **Шаг B1 — Инфраструктура и БД**:
   - Создать `docker-compose.dev.yml`, `docker-compose.prod.yml`, `deploy/nginx/default.conf`, `.env.example`.
   - Написать миграции `db/migrations/00001_init_schema.sql` + триггеры `00002_triggers.sql` и очистить устаревшие разрозненные SQL-файлы с эмбеддингами.
   - Поднять каркас `backend/` (`go.mod`, конфиг, `pgxpool`, `redis`, `minio`, `goose`, `cmd/seed` со шкалой КЕГЭ 2026, тарифами и демо-задачами).
2. **Шаг B2 — Auth, Users & Subscriptions**:
   - OAuth Яндекс + VK, JWT + Redis refresh куки, middleware (`Auth`, `Impersonation`, `RateLimit`, `SubscriptionGuard`).
   - Эндпоинты онбординга ФИ/ника, тарифов и активации инвайт-ссылок `/subscriptions/invites/{code}/claim`.
3. **Шаг B3 — Tasks, Grader, Importer & Student Bank**:
   - Полиморфный `Grader` (4 типа ответов + unit-тесты на все крайние случаи №19–21, №25, №26–27).
   - Публичный/авторизованный Банк задач (фильтр активных пробников, раскрытие разбора после 3 попыток, обновление гистограммы раз в 7 дней).
   - Дашборд ученика + алгоритм рекомендаций (`weak_topic`, `adaptive_difficulty`).
   - Админский CRUD задач, загрузка медиа в S3 и CSV/ZIP-импортёр с `?dry_run=true`.
4. **Шаг B4 — Rooms, Assignments & Self-Mock Exams**:
   - CRUD групп, инвайт-коды, проверка квот тарифа и Read-Only режим при истёкшей подписке.
   - Конструктор работ всех 5 режимов (`homework`, `classwork`, `practice`, `proctored_test`, `mock_exam`) + генератор самостоятельных пробников ученика на 27 номеров КИМ.
   - Жизненный цикл сессии (`start`, `answer` с сохранением кода в приватный S3, `finish` с переводом баллов 29 → 100).
5. **Шаг B5 — WebSocket Engine, Proctoring & Background Jobs**:
   - Двухуровневый `ws.Hub` (Live-матрица + On-Demand трансляция кода ученика `server.live_stream_toggle`).
   - `TelemetryBatcher` для быстрой пакетной записи в партиции `user_task_events`.
   - Эндпоинты учителя `/assignments/{id}/matrix` и `/sessions/{session_id}/inspect` + планировщик `internal/jobs`.

