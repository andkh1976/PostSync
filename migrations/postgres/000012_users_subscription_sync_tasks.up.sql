ALTER TABLE users ADD COLUMN IF NOT EXISTS subscription_end TIMESTAMP;
ALTER TABLE users ADD COLUMN IF NOT EXISTS balance INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS sync_tasks (
    id             BIGSERIAL PRIMARY KEY,
    user_id        BIGINT NOT NULL,
    status         TEXT   NOT NULL DEFAULT 'pending',
    start_date     TIMESTAMP,
    end_date       TIMESTAMP,
    last_synced_id TEXT
);
