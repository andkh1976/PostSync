CREATE TABLE IF NOT EXISTS max_channel_confirmations (
    id BIGSERIAL PRIMARY KEY,
    tg_user_id BIGINT NOT NULL,
    max_chat_id BIGINT NOT NULL,
    code TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    confirmed_at TIMESTAMPTZ,
    used_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_max_channel_confirmations_user_chat
    ON max_channel_confirmations(tg_user_id, max_chat_id, status, expires_at);
