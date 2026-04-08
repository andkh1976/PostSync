CREATE TABLE IF NOT EXISTS user_mtproto_sessions (
    user_id BIGINT PRIMARY KEY,
    session_data BYTEA NOT NULL,
    updated_at BIGINT NOT NULL
);
