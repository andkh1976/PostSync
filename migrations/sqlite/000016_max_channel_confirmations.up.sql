CREATE TABLE IF NOT EXISTS max_channel_confirmations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tg_user_id INTEGER NOT NULL,
    max_chat_id INTEGER NOT NULL,
    code TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    confirmed_at TEXT,
    used_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_max_channel_confirmations_user_chat
    ON max_channel_confirmations(tg_user_id, max_chat_id, status, expires_at);
