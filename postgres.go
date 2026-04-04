package main

import (
        "database/sql"
        "log/slog"
        "sync"
        "time"

        _ "github.com/lib/pq"
)

type pgRepo struct {
        db *sql.DB
        mu sync.Mutex
}

func NewPostgresRepo(dsn string) (Repository, error) {
        db, err := sql.Open("postgres", dsn)
        if err != nil {
                return nil, err
        }
        if err := db.Ping(); err != nil {
                return nil, err
        }

        if err := runMigrations(db, "postgres"); err != nil {
                return nil, err
        }

        return &pgRepo{db: db}, nil
}

func (r *pgRepo) Register(key, platform string, chatID int64) (bool, string, error) {
        r.mu.Lock()
        defer r.mu.Unlock()

        if key == "" {
                var existing string
                err := r.db.QueryRow("SELECT key FROM pending WHERE platform = $1 AND chat_id = $2 AND command = 'bridge'", platform, chatID).Scan(&existing)
                if err == nil {
                        return false, existing, nil
                }
                generated := genKey()
                _, err = r.db.Exec("INSERT INTO pending (key, platform, chat_id, created_at, command) VALUES ($1, $2, $3, $4, 'bridge')", generated, platform, chatID, time.Now().Unix())
                return false, generated, err
        }

        var peerPlatform string
        var peerChatID int64
        err := r.db.QueryRow("SELECT platform, chat_id FROM pending WHERE key = $1 AND command = 'bridge'", key).Scan(&peerPlatform, &peerChatID)
        if err != nil {
                return false, "", nil
        }
        if peerPlatform == platform {
                return false, "", nil
        }

        r.db.Exec("DELETE FROM pending WHERE key = $1", key)

        var tgID, maxID int64
        if platform == "tg" {
                tgID, maxID = chatID, peerChatID
        } else {
                tgID, maxID = peerChatID, chatID
        }

        _, err = r.db.Exec(
                "INSERT INTO pairs (tg_chat_id, max_chat_id) VALUES ($1, $2) ON CONFLICT (tg_chat_id, max_chat_id) DO NOTHING",
                tgID, maxID)
        return true, "", err
}

func (r *pgRepo) GetMaxChat(tgChatID int64) (int64, bool) {
        var id int64
        err := r.db.QueryRow("SELECT max_chat_id FROM pairs WHERE tg_chat_id = $1", tgChatID).Scan(&id)
        return id, err == nil
}

func (r *pgRepo) GetTgChat(maxChatID int64) (int64, bool) {
        var id int64
        err := r.db.QueryRow("SELECT tg_chat_id FROM pairs WHERE max_chat_id = $1", maxChatID).Scan(&id)
        return id, err == nil
}

func (r *pgRepo) SaveMsg(tgChatID int64, tgMsgID int, maxChatID int64, maxMsgID string) {
        r.db.Exec(
                `INSERT INTO messages (tg_chat_id, tg_msg_id, max_chat_id, max_msg_id, created_at)
                 VALUES ($1, $2, $3, $4, $5)
                 ON CONFLICT (tg_chat_id, tg_msg_id) DO UPDATE
                 SET max_chat_id = EXCLUDED.max_chat_id, max_msg_id = EXCLUDED.max_msg_id, created_at = EXCLUDED.created_at`,
                tgChatID, tgMsgID, maxChatID, maxMsgID, time.Now().Unix())
}

func (r *pgRepo) LookupMaxMsgID(tgChatID int64, tgMsgID int) (string, bool) {
        var id string
        err := r.db.QueryRow("SELECT max_msg_id FROM messages WHERE tg_chat_id = $1 AND tg_msg_id = $2", tgChatID, tgMsgID).Scan(&id)
        return id, err == nil
}

func (r *pgRepo) LookupTgMsgID(maxMsgID string) (int64, int, bool) {
        var chatID int64
        var msgID int
        err := r.db.QueryRow("SELECT tg_chat_id, tg_msg_id FROM messages WHERE max_msg_id = $1", maxMsgID).Scan(&chatID, &msgID)
        return chatID, msgID, err == nil
}

func (r *pgRepo) CleanOldMessages() {
        r.db.Exec("DELETE FROM messages WHERE created_at < $1", time.Now().Unix()-48*3600)
        r.db.Exec("DELETE FROM pending WHERE created_at > 0 AND created_at < $1", time.Now().Unix()-3600)
}

// IsCrosspostOwnerByTgChat проверяет, является ли userID владельцем связки по tgChatID.
// Возвращает true если связка старая (owner_id=0 и tg_owner_id=0 — legacy без владельца).
func (r *pgRepo) IsCrosspostOwnerByTgChat(tgChatID, userID int64) bool {
        var maxOwner, tgOwner int64
        err := r.db.QueryRow(
                "SELECT owner_id, tg_owner_id FROM crossposts WHERE tg_chat_id = $1 AND deleted_at = 0",
                tgChatID,
        ).Scan(&maxOwner, &tgOwner)
        if err != nil {
                return false
        }
        if maxOwner == 0 && tgOwner == 0 {
                return true // legacy-связка без явного владельца
        }
        return userID == maxOwner || userID == tgOwner
}

// ClearMessagesMapping удаляет все записи из messages для tgChatID.
func (r *pgRepo) ClearMessagesMapping(tgChatID int64) error {
        res, err := r.db.Exec("DELETE FROM messages WHERE tg_chat_id = $1", tgChatID)
        if err != nil {
                return err
        }
        n, _ := res.RowsAffected()
        slog.Info("ClearMessagesMapping",
                "tg_chat_id", tgChatID,
                "deleted", n,
        )
        return nil
}

func (r *pgRepo) HasPrefix(platform string, chatID int64) bool {
        var v int
        var err error
        if platform == "tg" {
                err = r.db.QueryRow("SELECT prefix FROM pairs WHERE tg_chat_id = $1", chatID).Scan(&v)
        } else {
                err = r.db.QueryRow("SELECT prefix FROM pairs WHERE max_chat_id = $1", chatID).Scan(&v)
        }
        if err != nil {
                return true
        }
        return v == 1
}

func (r *pgRepo) SetPrefix(platform string, chatID int64, on bool) bool {
        v := 0
        if on {
                v = 1
        }
        var res sql.Result
        if platform == "tg" {
                res, _ = r.db.Exec("UPDATE pairs SET prefix = $1 WHERE tg_chat_id = $2", v, chatID)
        } else {
                res, _ = r.db.Exec("UPDATE pairs SET prefix = $1 WHERE max_chat_id = $2", v, chatID)
        }
        if res == nil {
                return false
        }
        n, _ := res.RowsAffected()
        return n > 0
}

func (r *pgRepo) Unpair(platform string, chatID int64) bool {
        r.mu.Lock()
        defer r.mu.Unlock()
        var res sql.Result
        if platform == "tg" {
                res, _ = r.db.Exec("DELETE FROM pairs WHERE tg_chat_id = $1", chatID)
        } else {
                res, _ = r.db.Exec("DELETE FROM pairs WHERE max_chat_id = $1", chatID)
        }
        if res == nil {
                return false
        }
        n, _ := res.RowsAffected()
        return n > 0
}

func (r *pgRepo) PairCrosspost(tgChatID, maxChatID, ownerID, tgOwnerID int64) error {
        now := time.Now().Unix()
        _, err := r.db.Exec(
                "INSERT INTO crossposts (tg_chat_id, max_chat_id, created_at, owner_id, tg_owner_id) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (tg_chat_id, max_chat_id) DO UPDATE SET deleted_at = 0, deleted_by = 0, created_at = $3, owner_id = $4, tg_owner_id = $5",
                tgChatID, maxChatID, now, ownerID, tgOwnerID)
        return err
}

func (r *pgRepo) GetCrosspostOwner(maxChatID int64) (maxOwner, tgOwner int64) {
        r.db.QueryRow("SELECT owner_id, tg_owner_id FROM crossposts WHERE max_chat_id = $1 AND deleted_at = 0", maxChatID).Scan(&maxOwner, &tgOwner)
        return
}

func (r *pgRepo) GetCrosspostMaxChat(tgChatID int64, ownerID int64) (int64, string, bool) {
        var id int64
        var dir string
        err := r.db.QueryRow(
                "SELECT max_chat_id, direction FROM crossposts WHERE tg_chat_id = $1 AND ($2 = 0 OR tg_owner_id = $2 OR owner_id = $2) AND deleted_at = 0 ORDER BY created_at ASC",
                tgChatID, ownerID).Scan(&id, &dir)
        return id, dir, err == nil
}

func (r *pgRepo) GetCrosspostTgChat(maxChatID int64, ownerID int64) (int64, string, bool) {
        var id int64
        var dir string
        err := r.db.QueryRow(
                "SELECT tg_chat_id, direction FROM crossposts WHERE max_chat_id = $1 AND ($2 = 0 OR owner_id = $2 OR tg_owner_id = $2) AND deleted_at = 0 ORDER BY created_at ASC",
                maxChatID, ownerID).Scan(&id, &dir)
        return id, dir, err == nil
}

func (r *pgRepo) ListCrossposts(ownerID int64) []CrosspostLink {
        rows, err := r.db.Query("SELECT tg_chat_id, max_chat_id, direction FROM crossposts WHERE (owner_id = $1 OR tg_owner_id = $1 OR (owner_id = 0 AND tg_owner_id = 0)) AND deleted_at = 0", ownerID)
        if err != nil {
                return nil
        }
        defer rows.Close()
        var links []CrosspostLink
        for rows.Next() {
                var l CrosspostLink
                if rows.Scan(&l.TgChatID, &l.MaxChatID, &l.Direction) == nil {
                        links = append(links, l)
                }
        }
        return links
}

func (r *pgRepo) SetCrosspostDirection(maxChatID int64, direction string) bool {
        res, _ := r.db.Exec("UPDATE crossposts SET direction = $1 WHERE max_chat_id = $2 AND deleted_at = 0", direction, maxChatID)
        if res == nil {
                return false
        }
        n, _ := res.RowsAffected()
        return n > 0
}

func (r *pgRepo) UnpairCrosspost(maxChatID, deletedBy int64) bool {
        r.mu.Lock()
        defer r.mu.Unlock()
        res, _ := r.db.Exec("UPDATE crossposts SET deleted_at = $1, deleted_by = $2 WHERE max_chat_id = $3 AND deleted_at = 0",
                time.Now().Unix(), deletedBy, maxChatID)
        if res == nil {
                return false
        }
        n, _ := res.RowsAffected()
        return n > 0
}

func (r *pgRepo) GetCrosspostReplacements(maxChatID int64) CrosspostReplacements {
        var raw string
        r.db.QueryRow("SELECT replacements FROM crossposts WHERE max_chat_id = $1 AND deleted_at = 0", maxChatID).Scan(&raw)
        return parseCrosspostReplacements(raw)
}

func (r *pgRepo) SetCrosspostReplacements(maxChatID int64, repl CrosspostReplacements) error {
        data := marshalCrosspostReplacements(repl)
        r.mu.Lock()
        defer r.mu.Unlock()
        _, err := r.db.Exec("UPDATE crossposts SET replacements = $1 WHERE max_chat_id = $2 AND deleted_at = 0", data, maxChatID)
        return err
}

func (r *pgRepo) TouchUser(userID int64, platform, username, firstName string) {
        now := time.Now().Unix()
        r.db.Exec(`INSERT INTO users (user_id, platform, username, first_name, first_seen, last_seen) VALUES ($1, $2, $3, $4, $5, $5)
                ON CONFLICT(user_id) DO UPDATE SET username=EXCLUDED.username, first_name=EXCLUDED.first_name, last_seen=EXCLUDED.last_seen`,
                userID, platform, username, firstName, now)
}

func (r *pgRepo) ListUsers(platform string) ([]int64, error) {
        rows, err := r.db.Query("SELECT user_id FROM users WHERE platform = $1", platform)
        if err != nil {
                return nil, err
        }
        defer rows.Close()
        var ids []int64
        for rows.Next() {
                var id int64
                if rows.Scan(&id) == nil {
                        ids = append(ids, id)
                }
        }
        return ids, nil
}

func (r *pgRepo) EnqueueSend(item *QueueItem) error {
        _, err := r.db.Exec(
                `INSERT INTO send_queue (direction, src_chat_id, dst_chat_id, src_msg_id, text, att_type, att_token, reply_to, format, att_url, parse_mode, attempts, created_at, next_retry)
                 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 0, $12, $13)`,
                item.Direction, item.SrcChatID, item.DstChatID, item.SrcMsgID,
                item.Text, item.AttType, item.AttToken, item.ReplyTo, item.Format,
                item.AttURL, item.ParseMode,
                item.CreatedAt, item.NextRetry,
        )
        return err
}

func (r *pgRepo) PeekQueue(limit int) ([]QueueItem, error) {
        rows, err := r.db.Query(
                `SELECT id, direction, src_chat_id, dst_chat_id, src_msg_id, text, att_type, att_token, reply_to, format, att_url, parse_mode, attempts, created_at, next_retry
                 FROM send_queue WHERE next_retry <= $1 ORDER BY id ASC LIMIT $2`,
                time.Now().Unix(), limit,
        )
        if err != nil {
                return nil, err
        }
        defer rows.Close()
        var items []QueueItem
        for rows.Next() {
                var q QueueItem
                if err := rows.Scan(&q.ID, &q.Direction, &q.SrcChatID, &q.DstChatID, &q.SrcMsgID,
                        &q.Text, &q.AttType, &q.AttToken, &q.ReplyTo, &q.Format,
                        &q.AttURL, &q.ParseMode,
                        &q.Attempts, &q.CreatedAt, &q.NextRetry); err != nil {
                        return nil, err
                }
                items = append(items, q)
        }
        return items, nil
}

func (r *pgRepo) DeleteFromQueue(id int64) error {
        _, err := r.db.Exec("DELETE FROM send_queue WHERE id = $1", id)
        return err
}

func (r *pgRepo) IncrementAttempt(id int64, nextRetry int64) error {
        _, err := r.db.Exec("UPDATE send_queue SET attempts = attempts + 1, next_retry = $1 WHERE id = $2", nextRetry, id)
        return err
}

func (r *pgRepo) GetPendingSyncTasks() ([]SyncTask, error) {
        rows, err := r.db.Query(
                `SELECT id, user_id, tg_chat_id, max_chat_id, status, start_date, end_date, last_synced_id, error
                 FROM sync_tasks WHERE status = 'pending' ORDER BY id ASC`,
        )
        if err != nil {
                return nil, err
        }
        defer rows.Close()
        var tasks []SyncTask
        for rows.Next() {
                var t SyncTask
                var lastID, errMsg *string
                if err := rows.Scan(&t.ID, &t.UserID, &t.TgChatID, &t.MaxChatID, &t.Status,
                        &t.StartDate, &t.EndDate, &lastID, &errMsg); err != nil {
                        return nil, err
                }
                if lastID != nil {
                        t.LastSyncedID = *lastID
                }
                if errMsg != nil {
                        t.Error = *errMsg
                }
                tasks = append(tasks, t)
        }
        return tasks, nil
}

func (r *pgRepo) SetSyncTaskStatus(id int64, status, errMsg string) error {
        _, err := r.db.Exec(
                `UPDATE sync_tasks SET status = $1, error = $2 WHERE id = $3`,
                status, errMsg, id,
        )
        return err
}

func (r *pgRepo) UpdateSyncTaskLastID(id int64, lastSyncedID string) error {
        _, err := r.db.Exec(
                `UPDATE sync_tasks SET last_synced_id = $1 WHERE id = $2`,
                lastSyncedID, id,
        )
        return err
}

func (r *pgRepo) DeleteCompletedSyncTasks(userID int64) error {
        _, err := r.db.Exec(
                `DELETE FROM sync_tasks WHERE user_id = $1 AND status IN ('done', 'failed', 'cancelled')`,
                userID,
        )
        return err
}

func (r *pgRepo) SetCrosspostLiveListen(maxChatID int64, liveListen bool) bool {
        res, _ := r.db.Exec("UPDATE crossposts SET live_listen = $1 WHERE max_chat_id = $2 AND deleted_at = 0", liveListen, maxChatID)
        if res == nil {
                return false
        }
        n, _ := res.RowsAffected()
        return n > 0
}

func (r *pgRepo) GetCrosspostLiveListen(maxChatID int64) bool {
        var v bool
        r.db.QueryRow("SELECT live_listen FROM crossposts WHERE max_chat_id = $1 AND deleted_at = 0", maxChatID).Scan(&v)
        return v
}

func (r *pgRepo) CreateSyncTask(task SyncTask) (int64, error) {
        var id int64
        err := r.db.QueryRow(
                `INSERT INTO sync_tasks (user_id, tg_chat_id, max_chat_id, status, start_date, end_date, last_synced_id, error)
                 VALUES ($1, $2, $3, 'pending', $4, $5, '', '') RETURNING id`,
                task.UserID, task.TgChatID, task.MaxChatID, task.StartDate, task.EndDate,
        ).Scan(&id)
        return id, err
}

// GetUserProfile — Sprint 4: возвращает профиль пользователя из таблицы users.
func (r *pgRepo) GetUserProfile(userID int64) (*UserProfile, error) {
        var p UserProfile
        var subEnd *time.Time
        err := r.db.QueryRow(
                `SELECT user_id, platform, COALESCE(username,''), COALESCE(first_name,''), subscription_end FROM users WHERE user_id = $1`,
                userID,
        ).Scan(&p.UserID, &p.Platform, &p.Username, &p.FirstName, &subEnd)
        if err != nil {
                return nil, err
        }
        if subEnd != nil {
                p.SubscriptionEnd = subEnd
                p.HasSubscription = subEnd.After(time.Now())
        }
        return &p, nil
}

// ListUserSyncTasks — Sprint 4: возвращает все задачи синхронизации пользователя.
func (r *pgRepo) ListUserSyncTasks(userID int64) ([]SyncTask, error) {
        rows, err := r.db.Query(
                `SELECT id, user_id, tg_chat_id, max_chat_id, status, start_date, end_date, last_synced_id, error
                 FROM sync_tasks WHERE user_id = $1 ORDER BY id DESC LIMIT 20`,
                userID,
        )
        if err != nil {
                return nil, err
        }
        defer rows.Close()
        var tasks []SyncTask
        for rows.Next() {
                var t SyncTask
                var lastID, errMsg *string
                if err := rows.Scan(&t.ID, &t.UserID, &t.TgChatID, &t.MaxChatID, &t.Status,
                        &t.StartDate, &t.EndDate, &lastID, &errMsg); err != nil {
                        return nil, err
                }
                if lastID != nil {
                        t.LastSyncedID = *lastID
                }
                if errMsg != nil {
                        t.Error = *errMsg
                }
                tasks = append(tasks, t)
        }
        return tasks, nil
}

func (r *pgRepo) UpsertMaxKnownChat(chat MaxKnownChat) {
        r.db.Exec(
                `INSERT INTO max_known_chats (chat_id, title, chat_type, updated_at)
                 VALUES ($1, $2, $3, $4)
                 ON CONFLICT (chat_id) DO UPDATE SET title=EXCLUDED.title, chat_type=EXCLUDED.chat_type, updated_at=EXCLUDED.updated_at`,
                chat.ChatID, chat.Title, chat.ChatType, time.Now().Unix(),
        )
}

func (r *pgRepo) ListMaxKnownChats() []MaxKnownChat {
        rows, err := r.db.Query(`SELECT chat_id, title, chat_type FROM max_known_chats ORDER BY updated_at DESC`)
        if err != nil {
                return nil
        }
        defer rows.Close()
        var chats []MaxKnownChat
        for rows.Next() {
                var c MaxKnownChat
                if err := rows.Scan(&c.ChatID, &c.Title, &c.ChatType); err == nil {
                        chats = append(chats, c)
                }
        }
        return chats
}

func (r *pgRepo) Close() error {
        return r.db.Close()
}
