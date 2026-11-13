-- =============================================================================
-- EXTENSIONS
-- =============================================================================
-- vector      — pgvector: хранение и поиск векторных эмбеддингов задач (HNSW, cosine).
-- pg_partman  — автоматическое управление партициями user_task_event по месяцам.
-- pg_cron     — планировщик cron-заданий внутри PostgreSQL: агрегация, score, partman.
-- pg_trgm     — trigram-индексы для полнотекстового поиска задач по тексту (GIN).
-- hll         — HyperLogLog: approximate COUNT DISTINCT для bank_stats_count / test_stats_count
--               в cron-job пересчёта агрегатов задач. Погрешность ~1–2% приемлема.

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_partman SCHEMA partman;
CREATE EXTENSION IF NOT EXISTS pg_cron;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS hll;

-- =============================================================================
-- APPLY ALL SCRIPT
-- =============================================================================
\i types.sql
\i functions.sql
\i user.sql
\i subject.sql
\i topic.sql
\i task.sql
\i user_task.sql
\i user_topic_stat.sql
\i task_recommendation_log.sql
\i room.sql
\i user_room.sql
\i test.sql
\i test_task.sql
\i user_task_event.sql
\i user_room_test_task.sql
\i test_task_stat.sql
\i cron.sql