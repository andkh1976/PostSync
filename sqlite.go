package main

import (
	"database/sql"
	"log/slog"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteRepo struct {
	db *sql.DB
	mu sync.Mutex
}

func NewSQLiteRepo(dbPath string) (Repository, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}

	if err := runMigrations(db, "sqlite"); err != nil {
		return nil, err
	}

	return &sqliteRepo{db: db}, nil
}

func (r *sqliteRepo) Register(key, platform string, chatID int64) (bool, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if key == "" {
		var existing string
		err := r.db.QueryRow("SELECT key FROM pending WHERE platform = ? AND chat_id = ? AND command = 'bridge'", platform, chatID).Scan(&existing)
		if err == nil {
			return false, existing, nil
		}
		generated := genKey()
		_, err = r.db.Exec("INSERT INTO pending (key, platform, chat_id, created_at, command) VALUES (?, ?, ?, ?, 'bridge')", generated, platform, chatID, time.Now().Unix())
		return false, generated, err
	}

	var peerPlatform string
	var peerChatID int64
	err := r.db.QueryRow("SELECT platform, chat_id FROM pending WHERE key = ? AND command = 'bridge'", key).Scan(&peerPlatform, &peerChatID)
	if err != nil {
		return false, "", nil
	}
	if peerPlatform == platform {
		return false, "", nil
	}

	r.db.Exec("DELETE FROM pending WHERE key = ?", key)

	var tgID, maxID int64
	if platform == "tg" {
		tgID, maxID = chatID, peerChatID
	} else {
		tgID, maxID = peerChatID, chatID
	}

	_, err = r.db.Exec("INSERT OR REPLACE INTO pairs (tg_chat_id, max_chat_id) VALUES (?, ?)", tgID, maxID)
	return true, "", err
}

func (r *sqliteRepo) GetMaxChat(tgChatID int64) (int64, bool) {
	var id int64
	err := r.db.QueryRow("SELECT max_chat_id FROM pairs WHERE tg_chat_id = ?", tgChatID).Scan(&id)
	return id, err == nil
}

func (r *sqliteRepo) GetTgChat(maxChatID int64) (int64, bool) {
	var id int64
	err := r.db.QueryRow("SELECT tg_chat_id FROM pairs WHERE max_chat_id = ?", maxChatID).Scan(&id)
	return id, err == nil
}

func (r *sqliteRepo) SaveMsg(tgChatID int64, tgMsgID int, maxChatID int64, maxMsgID string) {
	r.db.Exec("INSERT OR REPLACE INTO messages (tg_chat_id, tg_msg_id, max_chat_id, max_msg_id, created_at) VALUES (?, ?, ?, ?, ?)",
		tgChatID, tgMsgID, maxChatID, maxMsgID, time.Now().Unix())
}

func (r *sqliteRepo) LookupMaxMsgID(tgChatID int64, tgMsgID int) (string, bool) {
	var id string
	err := r.db.QueryRow("SELECT max_msg_id FROM messages WHERE tg_chat_id = ? AND tg_msg_id = ?", tgChatID, tgMsgID).Scan(&id)
	return id, err == nil
}

func (r *sqliteRepo) LookupTgMsgID(maxMsgID string) (int64, int, bool) {
	var chatID int64
	var msgID int
	err := r.db.QueryRow("SELECT tg_chat_id, tg_msg_id FROM messages WHERE max_msg_id = ?", maxMsgID).Scan(&chatID, &msgID)
	return chatID, msgID, err == nil
}

func (r *sqliteRepo) CleanOldMessages() {
	r.db.Exec("DELETE FROM messages WHERE created_at < ?", time.Now().Unix()-48*3600)
	r.db.Exec("DELETE FROM pending WHERE created_at > 0 AND created_at < ?", time.Now().Unix()-3600)
}

// IsCrosspostOwnerByTgChat проверяет, является ли userID владельцем связки по tgChatID.
// Возвращает true если связка старая (owner_id=0 и tg_owner_id=0 — legacy без владельца).
func (r *sqliteRepo) IsCrosspostOwnerByTgChat(tgChatID, userID int64) bool {
	var maxOwner, tgOwner int64
	err := r.db.QueryRow(
		"SELECT owner_id, tg_owner_id FROM crossposts WHERE tg_chat_id = ? AND deleted_at = 0",
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
func (r *sqliteRepo) ClearMessagesMapping(tgChatID int64) error {
	res, err := r.db.Exec("DELETE FROM messages WHERE tg_chat_id = ?", tgChatID)
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

func (r *sqliteRepo) HasPrefix(platform string, chatID int64) bool {
	var v int
	var err error
	if platform == "tg" {
		err = r.db.QueryRow("SELECT prefix FROM pairs WHERE tg_chat_id = ?", chatID).Scan(&v)
	} else {
		err = r.db.QueryRow("SELECT prefix FROM pairs WHERE max_chat_id = ?", chatID).Scan(&v)
	}
	if err != nil {
		return true
	}
	return v == 1
}

func (r *sqliteRepo) SetPrefix(platform string, chatID int64, on bool) bool {
	v := 0
	if on {
		v = 1
	}
	var res sql.Result
	if platform == "tg" {
		res, _ = r.db.Exec("UPDATE pairs SET prefix = ? WHERE tg_chat_id = ?", v, chatID)
	} else {
		res, _ = r.db.Exec("UPDATE pairs SET prefix = ? WHERE max_chat_id = ?", v, chatID)
	}
	if res == nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (r *sqliteRepo) Unpair(platform string, chatID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	var res sql.Result
	if platform == "tg" {
		res, _ = r.db.Exec("DELETE FROM pairs WHERE tg_chat_id = ?", chatID)
	} else {
		res, _ = r.db.Exec("DELETE FROM pairs WHERE max_chat_id = ?", chatID)
	}
	if res == nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (r *sqliteRepo) PairCrosspost(tgChatID, maxChatID, ownerID, tgOwnerID int64) error {
	_, err := r.db.Exec("INSERT OR REPLACE INTO crossposts (tg_chat_id, max_chat_id, created_at, owner_id, tg_owner_id) VALUES (?, ?, ?, ?, ?)",
		tgChatID, maxChatID, time.Now().Unix(), ownerID, tgOwnerID)
	return err
}

func (r *sqliteRepo) GetCrosspostOwner(maxChatID int64) (maxOwner, tgOwner int64) {
	r.db.QueryRow("SELECT owner_id, tg_owner_id FROM crossposts WHERE max_chat_id = ? AND deleted_at = 0", maxChatID).Scan(&maxOwner, &tgOwner)
	return
}

func (r *sqliteRepo) GetCrosspostMaxChat(tgChatID int64, ownerID int64) (int64, string, bool) {
	var id int64
	var dir string
	err := r.db.QueryRow(
		"SELECT max_chat_id, direction FROM crossposts WHERE tg_chat_id = ? AND (? = 0 OR tg_owner_id = ? OR owner_id = ?) AND deleted_at = 0 ORDER BY created_at ASC",
		tgChatID, ownerID, ownerID, ownerID).Scan(&id, &dir)
	return id, dir, err == nil
}

func (r *sqliteRepo) GetCrosspostTgChat(maxChatID int64, ownerID int64) (int64, string, bool) {
	var id int64
	var dir string
	err := r.db.QueryRow(
		"SELECT tg_chat_id, direction FROM crossposts WHERE max_chat_id = ? AND (? = 0 OR owner_id = ? OR tg_owner_id = ?) AND deleted_at = 0 ORDER BY created_at ASC",
		maxChatID, ownerID, ownerID, ownerID).Scan(&id, &dir)
	return id, dir, err == nil
}

func (r *sqliteRepo) ListCrossposts(ownerID int64) []CrosspostLink {
	rows, err := r.db.Query("SELECT tg_chat_id, max_chat_id, direction FROM crossposts WHERE (owner_id = ? OR tg_owner_id = ? OR (owner_id = 0 AND tg_owner_id = 0)) AND deleted_at = 0", ownerID, ownerID)
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

func (r *sqliteRepo) SetCrosspostDirection(maxChatID int64, direction string) bool {
	res, _ := r.db.Exec("UPDATE crossposts SET direction = ? WHERE max_chat_id = ? AND deleted_at = 0", direction, maxChatID)
	if res == nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (r *sqliteRepo) UnpairCrosspost(maxChatID, deletedBy int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, _ := r.db.Exec("UPDATE crossposts SET deleted_at = ?, deleted_by = ? WHERE max_chat_id = ? AND deleted_at = 0",
		time.Now().Unix(), deletedBy, maxChatID)
	if res == nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (r *sqliteRepo) GetCrosspostReplacements(maxChatID int64) CrosspostReplacements {
	var raw string
	r.db.QueryRow("SELECT replacements FROM crossposts WHERE max_chat_id = ? AND deleted_at = 0", maxChatID).Scan(&raw)
	return parseCrosspostReplacements(raw)
}

func (r *sqliteRepo) SetCrosspostReplacements(maxChatID int64, repl CrosspostReplacements) error {
	data := marshalCrosspostReplacements(repl)
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec("UPDATE crossposts SET replacements = ? WHERE max_chat_id = ? AND deleted_at = 0", data, maxChatID)
	return err
}

func (r *sqliteRepo) TouchUser(userID int64, platform, username, firstName string) {
	now := time.Now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.db.Exec(`INSERT INTO users (user_id, platform, username, first_name, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
                ON CONFLICT(user_id) DO UPDATE SET username=excluded.username, first_name=excluded.first_name, last_seen=excluded.last_seen`,
		userID, platform, username, firstName, now, now)
}

func (r *sqliteRepo) ListUsers(platform string) ([]int64, error) {
	rows, err := r.db.Query("SELECT user_id FROM users WHERE platform = ?", platform)
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

func (r *sqliteRepo) EnqueueSend(item *QueueItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(
		`INSERT INTO send_queue (direction, src_chat_id, dst_chat_id, src_msg_id, text, att_type, att_token, reply_to, format, att_url, parse_mode, attempts, created_at, next_retry)
                 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`,
		item.Direction, item.SrcChatID, item.DstChatID, item.SrcMsgID,
		item.Text, item.AttType, item.AttToken, item.ReplyTo, item.Format,
		item.AttURL, item.ParseMode,
		item.CreatedAt, item.NextRetry,
	)
	return err
}

func (r *sqliteRepo) PeekQueue(limit int) ([]QueueItem, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows, err := r.db.Query(
		`SELECT id, direction, src_chat_id, dst_chat_id, src_msg_id, text, att_type, att_token, reply_to, format, att_url, parse_mode, attempts, created_at, next_retry
                 FROM send_queue WHERE next_retry <= ? ORDER BY id ASC LIMIT ?`,
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

func (r *sqliteRepo) DeleteFromQueue(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec("DELETE FROM send_queue WHERE id = ?", id)
	return err
}

func (r *sqliteRepo) IncrementAttempt(id int64, nextRetry int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec("UPDATE send_queue SET attempts = attempts + 1, next_retry = ? WHERE id = ?", nextRetry, id)
	return err
}

func (r *sqliteRepo) GetSyncTask(taskID int64) (*SyncTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var t SyncTask
	var startDate, endDate, lastID, errMsg *string
	err := r.db.QueryRow(
		`SELECT id, user_id, tg_chat_id, max_chat_id, status, start_date, end_date, last_synced_id, error
		 FROM sync_tasks WHERE id = ?`,
		taskID,
	).Scan(&t.ID, &t.UserID, &t.TgChatID, &t.MaxChatID, &t.Status,
		&startDate, &endDate, &lastID, &errMsg)
	if err != nil {
		return nil, err
	}
	if startDate != nil {
		t.StartDate, _ = time.Parse(time.RFC3339, *startDate)
	}
	if endDate != nil {
		t.EndDate, _ = time.Parse(time.RFC3339, *endDate)
	}
	if lastID != nil {
		t.LastSyncedID = *lastID
	}
	if errMsg != nil {
		t.Error = *errMsg
	}
	return &t, nil
}

func (r *sqliteRepo) GetPendingSyncTasks() ([]SyncTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
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
		var startDate, endDate, lastID, errMsg *string
		if err := rows.Scan(&t.ID, &t.UserID, &t.TgChatID, &t.MaxChatID, &t.Status,
			&startDate, &endDate, &lastID, &errMsg); err != nil {
			return nil, err
		}
		if startDate != nil {
			t.StartDate, _ = time.Parse(time.RFC3339, *startDate)
		}
		if endDate != nil {
			t.EndDate, _ = time.Parse(time.RFC3339, *endDate)
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

func (r *sqliteRepo) SetSyncTaskStatus(id int64, status, errMsg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(
		`UPDATE sync_tasks SET status = ?, error = ? WHERE id = ?`,
		status, errMsg, id,
	)
	return err
}

func (r *sqliteRepo) UpdateSyncTaskLastID(id int64, lastSyncedID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(
		`UPDATE sync_tasks SET last_synced_id = ? WHERE id = ?`,
		lastSyncedID, id,
	)
	return err
}

func (r *sqliteRepo) SetCrosspostLiveListen(maxChatID int64, liveListen bool) bool {
	v := 0
	if liveListen {
		v = 1
	}
	res, _ := r.db.Exec("UPDATE crossposts SET live_listen = ? WHERE max_chat_id = ? AND deleted_at = 0", v, maxChatID)
	if res == nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (r *sqliteRepo) GetCrosspostLiveListen(maxChatID int64) bool {
	var v int
	r.db.QueryRow("SELECT live_listen FROM crossposts WHERE max_chat_id = ? AND deleted_at = 0", maxChatID).Scan(&v)
	return v != 0
}

func (r *sqliteRepo) CreateSyncTask(task SyncTask) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.db.Exec(
		`INSERT INTO sync_tasks (user_id, tg_chat_id, max_chat_id, status, start_date, end_date, last_synced_id, error)
                 VALUES (?, ?, ?, 'pending', ?, ?, '', '')`,
		task.UserID, task.TgChatID, task.MaxChatID,
		task.StartDate.Format(time.RFC3339),
		task.EndDate.Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetUserProfile — Sprint 4: возвращает профиль пользователя из таблицы users.
func (r *sqliteRepo) GetUserProfile(userID int64) (*UserProfile, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var p UserProfile
	var subEnd *string
	err := r.db.QueryRow(
		`SELECT user_id, platform, COALESCE(username,''), COALESCE(first_name,''), subscription_end FROM users WHERE user_id = ?`,
		userID,
	).Scan(&p.UserID, &p.Platform, &p.Username, &p.FirstName, &subEnd)
	if err != nil {
		return nil, err
	}
	if subEnd != nil && *subEnd != "" {
		t, parseErr := time.Parse(time.RFC3339, *subEnd)
		if parseErr == nil {
			p.SubscriptionEnd = &t
			p.HasSubscription = t.After(time.Now())
		}
	}
	return &p, nil
}

func (r *sqliteRepo) DeleteCompletedSyncTasks(userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(
		`DELETE FROM sync_tasks WHERE user_id = ? AND status IN ('done', 'failed', 'cancelled')`,
		userID,
	)
	return err
}

// ListUserSyncTasks — Sprint 4: возвращает все задачи синхронизации пользователя.
func (r *sqliteRepo) ListUserSyncTasks(userID int64) ([]SyncTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows, err := r.db.Query(
		`SELECT id, user_id, tg_chat_id, max_chat_id, status, start_date, end_date, last_synced_id, error
                 FROM sync_tasks WHERE user_id = ? ORDER BY id DESC LIMIT 20`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []SyncTask
	for rows.Next() {
		var t SyncTask
		var startDate, endDate, lastID, errMsg *string
		if err := rows.Scan(&t.ID, &t.UserID, &t.TgChatID, &t.MaxChatID, &t.Status,
			&startDate, &endDate, &lastID, &errMsg); err != nil {
			return nil, err
		}
		if startDate != nil {
			t.StartDate, _ = time.Parse(time.RFC3339, *startDate)
		}
		if endDate != nil {
			t.EndDate, _ = time.Parse(time.RFC3339, *endDate)
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

func (r *sqliteRepo) UpsertMaxKnownChat(chat MaxKnownChat) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.db.Exec(
		`INSERT INTO max_known_chats (chat_id, title, chat_type, updated_at) VALUES (?, ?, ?, ?)
                 ON CONFLICT (chat_id) DO UPDATE SET title=excluded.title, chat_type=excluded.chat_type, updated_at=excluded.updated_at`,
		chat.ChatID, chat.Title, chat.ChatType, time.Now().Unix(),
	)
}

func (r *sqliteRepo) ListMaxKnownChats() []MaxKnownChat {
	r.mu.Lock()
	defer r.mu.Unlock()
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

func (r *sqliteRepo) CreateMaxChannelConfirmation(c MaxChannelConfirmation) (*MaxChannelConfirmation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res, err := r.db.Exec(
		`INSERT INTO max_channel_confirmations (tg_user_id, max_chat_id, code, status, created_at, expires_at, confirmed_at, used_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.TgUserID, c.MaxChatID, c.Code, c.Status,
		c.CreatedAt.Format(time.RFC3339), c.ExpiresAt.Format(time.RFC3339),
		timePtrToRFC3339(c.ConfirmedAt), timePtrToRFC3339(c.UsedAt),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	c.ID = id
	return &c, nil
}

func (r *sqliteRepo) GetMaxChannelConfirmationByCode(code string) (*MaxChannelConfirmation, error) {
	return r.scanMaxChannelConfirmationRow(
		r.db.QueryRow(`SELECT id, tg_user_id, max_chat_id, code, status, created_at, expires_at, confirmed_at, used_at
			FROM max_channel_confirmations WHERE code = ? ORDER BY id DESC LIMIT 1`, code),
	)
}

func (r *sqliteRepo) GetUsableMaxChannelConfirmation(tgUserID, maxChatID int64, now time.Time) (*MaxChannelConfirmation, error) {
	return r.scanMaxChannelConfirmationRow(
		r.db.QueryRow(`SELECT id, tg_user_id, max_chat_id, code, status, created_at, expires_at, confirmed_at, used_at
			FROM max_channel_confirmations
			WHERE tg_user_id = ? AND max_chat_id = ? AND status = ? AND expires_at > ?
			ORDER BY confirmed_at DESC, id DESC LIMIT 1`,
			tgUserID, maxChatID, MaxChannelConfirmationStatusConfirmed, now.Format(time.RFC3339)),
	)
}

func (r *sqliteRepo) MarkMaxChannelConfirmationConfirmed(code string, maxChatID int64, confirmedAt time.Time) (*MaxChannelConfirmation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(
		`UPDATE max_channel_confirmations
		 SET status = ?, max_chat_id = ?, confirmed_at = ?, used_at = NULL
		 WHERE code = ? AND status = ? AND expires_at > ?`,
		MaxChannelConfirmationStatusConfirmed, maxChatID, confirmedAt.Format(time.RFC3339),
		code, MaxChannelConfirmationStatusPending, confirmedAt.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return r.GetMaxChannelConfirmationByCode(code)
}

func (r *sqliteRepo) MarkMaxChannelConfirmationUsed(id int64, usedAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(
		`UPDATE max_channel_confirmations SET status = ?, used_at = ? WHERE id = ?`,
		MaxChannelConfirmationStatusUsed, usedAt.Format(time.RFC3339), id,
	)
	return err
}

func (r *sqliteRepo) ExpireMaxChannelConfirmations(now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(
		`UPDATE max_channel_confirmations SET status = ? WHERE status = ? AND expires_at <= ?`,
		MaxChannelConfirmationStatusExpired, MaxChannelConfirmationStatusPending, now.Format(time.RFC3339),
	)
	return err
}

func (r *sqliteRepo) scanMaxChannelConfirmationRow(scanner interface {
	Scan(dest ...interface{}) error
}) (*MaxChannelConfirmation, error) {
	var c MaxChannelConfirmation
	var createdAt string
	var expiresAt string
	var confirmedAt sql.NullString
	var usedAt sql.NullString
	err := scanner.Scan(&c.ID, &c.TgUserID, &c.MaxChatID, &c.Code, &c.Status, &createdAt, &expiresAt, &confirmedAt, &usedAt)
	if err != nil {
		return nil, err
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	c.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
	if confirmedAt.Valid {
		t, err := time.Parse(time.RFC3339, confirmedAt.String)
		if err == nil {
			c.ConfirmedAt = &t
		}
	}
	if usedAt.Valid {
		t, err := time.Parse(time.RFC3339, usedAt.String)
		if err == nil {
			c.UsedAt = &t
		}
	}
	return &c, nil
}

func timePtrToRFC3339(v *time.Time) interface{} {
	if v == nil {
		return nil
	}
	return v.Format(time.RFC3339)
}

func (r *sqliteRepo) GetMTProtoSession(userID int64) ([]byte, error) {
	var data []byte
	err := r.db.QueryRow("SELECT session_data FROM user_mtproto_sessions WHERE user_id = ?", userID).Scan(&data)
	return data, err
}

func (r *sqliteRepo) SaveMTProtoSession(userID int64, sessionData []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(
		`INSERT INTO user_mtproto_sessions (user_id, session_data, updated_at) 
                 VALUES (?, ?, ?) 
                 ON CONFLICT(user_id) DO UPDATE SET session_data=excluded.session_data, updated_at=excluded.updated_at`,
		userID, sessionData, time.Now().Unix(),
	)
	return err
}

func (r *sqliteRepo) GrantSubscription(userID int64, days int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Assuming subscription_end is stored as ISO8601 string in SQLite
	_, err := r.db.Exec(`UPDATE users SET subscription_end = CASE 
                WHEN subscription_end IS NULL OR subscription_end = '' OR datetime(subscription_end) < datetime('now') THEN strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '+' || ? || ' days')
                ELSE strftime('%Y-%m-%dT%H:%M:%SZ', subscription_end, '+' || ? || ' days') END 
                WHERE user_id = ?`, days, days, userID)
	return err
}

func (r *sqliteRepo) RevokeSubscription(userID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, err := r.db.Exec(`UPDATE users SET subscription_end = strftime('%Y-%m-%dT%H:%M:%SZ', 'now', '-1 hours') WHERE user_id = ?`, userID)
	return err
}

func (r *sqliteRepo) Close() error {
	return r.db.Close()
}
