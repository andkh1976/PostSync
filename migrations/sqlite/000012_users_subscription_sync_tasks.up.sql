ALTER TABLE users ADD COLUMN subscription_end TIMESTAMP;
ALTER TABLE users ADD COLUMN balance INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS sync_tasks (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id   INTEGER NOT NULL,
    status    TEXT    NOT NULL DEFAULT 'pending',
    start_date    TIMESTAMP,
    end_date      TIMESTAMP,
    last_synced_id TEXT
);
