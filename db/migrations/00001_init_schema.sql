-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pg_trgm;
-- +goose StatementEnd

-- -----------------------------------------------------------------------------
-- Enums
-- -----------------------------------------------------------------------------
CREATE TYPE user_role AS ENUM (
    'superuser',
    'teacher',
    'student'
);

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

-- -----------------------------------------------------------------------------
-- Users, Subscriptions, Invites
-- -----------------------------------------------------------------------------
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

-- -----------------------------------------------------------------------------
-- Topics, Tasks, Scale, and User Progress
-- -----------------------------------------------------------------------------
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
    owner_id           UUID          NULL REFERENCES users(id),
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

CREATE INDEX idx_tasks_text_trgm  ON tasks USING gin (lower(text_md) gin_trgm_ops) WHERE deleted_at IS NULL AND is_public_bank = TRUE;
CREATE INDEX idx_tasks_kim_gin    ON tasks USING gin (kim_numbers) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_topic      ON tasks (topic_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_difficulty ON tasks (difficulty) WHERE deleted_at IS NULL;
CREATE INDEX idx_tasks_owner      ON tasks (owner_id) WHERE owner_id IS NOT NULL AND deleted_at IS NULL;

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

-- -----------------------------------------------------------------------------
-- Rooms, Assignments, Sessions, and Telemetry Events
-- -----------------------------------------------------------------------------
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
    room_id          UUID            NULL REFERENCES rooms(id),
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

-- Declaratively partitioned by range on occurred_at
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

-- Initial partitions
CREATE TABLE user_task_events_2026_11 PARTITION OF user_task_events
    FOR VALUES FROM ('2026-11-01 00:00:00+00') TO ('2026-12-01 00:00:00+00');

CREATE TABLE user_task_events_2026_12 PARTITION OF user_task_events
    FOR VALUES FROM ('2026-12-01 00:00:00+00') TO ('2027-01-01 00:00:00+00');

CREATE TABLE user_task_events_default PARTITION OF user_task_events DEFAULT;

-- +goose Down
DROP TABLE IF EXISTS user_task_events CASCADE;
DROP TABLE IF EXISTS assignment_task_stats CASCADE;
DROP TABLE IF EXISTS user_assignment_tasks CASCADE;
DROP TABLE IF EXISTS assignment_sessions CASCADE;
DROP TABLE IF EXISTS assignment_tasks CASCADE;
DROP TABLE IF EXISTS assignments CASCADE;
DROP TABLE IF EXISTS user_rooms CASCADE;
DROP TABLE IF EXISTS rooms CASCADE;
DROP TABLE IF EXISTS task_recommendation_logs CASCADE;
DROP TABLE IF EXISTS user_topic_stats CASCADE;
DROP TABLE IF EXISTS user_tasks CASCADE;
DROP TABLE IF EXISTS exam_score_scale CASCADE;
DROP TABLE IF EXISTS tasks CASCADE;
DROP TABLE IF EXISTS topics CASCADE;
DROP TABLE IF EXISTS subscription_invite_uses CASCADE;
DROP TABLE IF EXISTS subscription_invites CASCADE;
DROP TABLE IF EXISTS user_subscriptions CASCADE;
DROP TABLE IF EXISTS subscription_tiers CASCADE;
DROP TABLE IF EXISTS users CASCADE;

DROP TYPE IF EXISTS event_type;
DROP TYPE IF EXISTS session_status;
DROP TYPE IF EXISTS assignment_mode;
DROP TYPE IF EXISTS recommendation_reason;
DROP TYPE IF EXISTS user_role;

DROP EXTENSION IF EXISTS pg_trgm;
