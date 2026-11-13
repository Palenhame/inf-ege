-- +goose Up

-- -----------------------------------------------------------------------------
-- 1. Function: set_updated_at()
-- -----------------------------------------------------------------------------
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- Triggers for updated_at
CREATE TRIGGER trg_users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_subscription_tiers_updated_at
    BEFORE UPDATE ON subscription_tiers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_topics_updated_at
    BEFORE UPDATE ON topics
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_tasks_updated_at
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_user_tasks_updated_at
    BEFORE UPDATE ON user_tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_user_topic_stats_updated_at
    BEFORE UPDATE ON user_topic_stats
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_rooms_updated_at
    BEFORE UPDATE ON rooms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_user_rooms_updated_at
    BEFORE UPDATE ON user_rooms
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_assignments_updated_at
    BEFORE UPDATE ON assignments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_user_assignment_tasks_updated_at
    BEFORE UPDATE ON user_assignment_tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TRIGGER trg_assignment_task_stats_updated_at
    BEFORE UPDATE ON assignment_task_stats
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -----------------------------------------------------------------------------
-- 2. Function: sync_user_topic_stat()
-- -----------------------------------------------------------------------------
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_user_topic_stat()
RETURNS TRIGGER AS $$
DECLARE
    _solved INT;
    _attempted INT;
    _success_rate NUMERIC(5, 2);
    _avg_time INT;
BEGIN
    WITH all_attempts AS (
        SELECT task_id, bool_or(is_correct) AS is_solved
        FROM (
            SELECT task_id, is_correct
            FROM user_tasks
            WHERE user_id = NEW.user_id AND topic_id = NEW.topic_id
            UNION ALL
            SELECT task_id, is_correct
            FROM user_assignment_tasks
            WHERE user_id = NEW.user_id AND topic_id = NEW.topic_id
        ) sub
        GROUP BY task_id
    )
    SELECT coalesce(count(*) FILTER (WHERE is_solved = true), 0)::INT,
           coalesce(count(*), 0)::INT,
           coalesce(round((count(*) FILTER (WHERE is_solved = true)::NUMERIC / NULLIF(count(*), 0)) * 100, 2), 0)
    INTO _solved, _attempted, _success_rate
    FROM all_attempts;

    SELECT coalesce(avg(time_spent_sec), 0)::INT
    INTO _avg_time
    FROM user_assignment_tasks
    WHERE user_id = NEW.user_id AND topic_id = NEW.topic_id;

    INSERT INTO user_topic_stats (
        user_id,
        topic_id,
        solved,
        attempted,
        success_rate,
        avg_time,
        stats_updated_at,
        updated_at
    )
    VALUES (
        NEW.user_id,
        NEW.topic_id,
        _solved,
        _attempted,
        _success_rate,
        _avg_time,
        now(),
        now()
    )
    ON CONFLICT (user_id, topic_id) DO UPDATE SET
        solved           = EXCLUDED.solved,
        attempted        = EXCLUDED.attempted,
        success_rate     = EXCLUDED.success_rate,
        avg_time         = EXCLUDED.avg_time,
        stats_updated_at = now(),
        updated_at       = now();

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_user_tasks_sync_topic_stat
    AFTER INSERT OR UPDATE ON user_tasks
    FOR EACH ROW EXECUTE FUNCTION sync_user_topic_stat();

CREATE TRIGGER trg_user_assignment_tasks_sync_topic_stat
    AFTER INSERT OR UPDATE ON user_assignment_tasks
    FOR EACH ROW EXECUTE FUNCTION sync_user_topic_stat();

-- -----------------------------------------------------------------------------
-- 3. Function: sync_assignment_task_stat()
-- -----------------------------------------------------------------------------
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_assignment_task_stat()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO assignment_task_stats (
        assignment_id,
        task_id,
        solved_count,
        attempted_count,
        avg_time_sec,
        avg_attempts,
        updated_at
    )
    SELECT NEW.assignment_id,
           NEW.task_id,
           coalesce(count(*) FILTER (WHERE is_correct = true), 0)::INT,
           coalesce(count(*), 0)::INT,
           coalesce(avg(time_spent_sec), 0)::INT,
           coalesce(round(avg(attempts_used), 2), 0)::NUMERIC(5, 2),
           now()
    FROM user_assignment_tasks
    WHERE assignment_id = NEW.assignment_id AND task_id = NEW.task_id
    GROUP BY NEW.assignment_id, NEW.task_id
    ON CONFLICT (assignment_id, task_id) DO UPDATE SET
        solved_count    = EXCLUDED.solved_count,
        attempted_count = EXCLUDED.attempted_count,
        avg_time_sec    = EXCLUDED.avg_time_sec,
        avg_attempts    = EXCLUDED.avg_attempts,
        updated_at      = now();

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER trg_user_assignment_tasks_sync_stat
    AFTER INSERT OR UPDATE ON user_assignment_tasks
    FOR EACH ROW EXECUTE FUNCTION sync_assignment_task_stat();

-- +goose Down
DROP TRIGGER IF EXISTS trg_user_assignment_tasks_sync_stat ON user_assignment_tasks;
DROP TRIGGER IF EXISTS trg_user_assignment_tasks_sync_topic_stat ON user_assignment_tasks;
DROP TRIGGER IF EXISTS trg_user_tasks_sync_topic_stat ON user_tasks;

DROP TRIGGER IF EXISTS trg_assignment_task_stats_updated_at ON assignment_task_stats;
DROP TRIGGER IF EXISTS trg_user_assignment_tasks_updated_at ON user_assignment_tasks;
DROP TRIGGER IF EXISTS trg_assignments_updated_at ON assignments;
DROP TRIGGER IF EXISTS trg_user_rooms_updated_at ON user_rooms;
DROP TRIGGER IF EXISTS trg_rooms_updated_at ON rooms;
DROP TRIGGER IF EXISTS trg_user_topic_stats_updated_at ON user_topic_stats;
DROP TRIGGER IF EXISTS trg_user_tasks_updated_at ON user_tasks;
DROP TRIGGER IF EXISTS trg_tasks_updated_at ON tasks;
DROP TRIGGER IF EXISTS trg_topics_updated_at ON topics;
DROP TRIGGER IF EXISTS trg_subscription_tiers_updated_at ON subscription_tiers;
DROP TRIGGER IF EXISTS trg_users_updated_at ON users;

DROP FUNCTION IF EXISTS sync_assignment_task_stat();
DROP FUNCTION IF EXISTS sync_user_topic_stat();
DROP FUNCTION IF EXISTS set_updated_at();
