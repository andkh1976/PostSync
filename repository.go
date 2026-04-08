package main

import "time"

// Replacement — одно правило замены текста.
// Target: "" или "all" — весь текст, "links" — только ссылки.
type Replacement struct {
        From   string `json:"from"`
        To     string `json:"to"`
        Regex  bool   `json:"regex"`
        Target string `json:"target,omitempty"`
}

// CrosspostReplacements — замены по направлениям.
type CrosspostReplacements struct {
        TgToMax []Replacement `json:"tg>max,omitempty"`
        MaxToTg []Replacement `json:"max>tg,omitempty"`
}

// CrosspostLink — одна связка кросспостинга.
type CrosspostLink struct {
        TgChatID  int64
        MaxChatID int64
        Direction string
}

// Repository — абстракция хранилища для bridge.
type Repository interface {
        // Register обрабатывает /bridge команду.
        // Без ключа — создаёт pending запись и возвращает сгенерированный ключ.
        // С ключом — ищет пару и создаёт связку.
        Register(key, platform string, chatID int64) (paired bool, generatedKey string, err error)

        GetMaxChat(tgChatID int64) (int64, bool)
        GetTgChat(maxChatID int64) (int64, bool)

        SaveMsg(tgChatID int64, tgMsgID int, maxChatID int64, maxMsgID string)
        LookupMaxMsgID(tgChatID int64, tgMsgID int) (string, bool)
        LookupTgMsgID(maxMsgID string) (int64, int, bool)
        CleanOldMessages()

        HasPrefix(platform string, chatID int64) bool
        SetPrefix(platform string, chatID int64, on bool) bool

        Unpair(platform string, chatID int64) bool

        // Crosspost methods
        PairCrosspost(tgChatID, maxChatID, ownerID, tgOwnerID int64) error
        GetCrosspostOwner(maxChatID int64) (maxOwner, tgOwner int64)
        // ownerID=0 means no owner filter (internal/system calls).
        GetCrosspostMaxChat(tgChatID int64, ownerID int64) (maxChatID int64, direction string, ok bool)
        // ownerID=0 means no owner filter (internal/system calls).
        GetCrosspostTgChat(maxChatID int64, ownerID int64) (tgChatID int64, direction string, ok bool)
        ListCrossposts(ownerID int64) []CrosspostLink
        SetCrosspostDirection(maxChatID int64, direction string) bool
        UnpairCrosspost(maxChatID, deletedBy int64) bool
        GetCrosspostReplacements(maxChatID int64) CrosspostReplacements
        SetCrosspostReplacements(maxChatID int64, repl CrosspostReplacements) error

        // IsCrosspostOwnerByTgChat проверяет, является ли userID владельцем связки
        // по tgChatID. Возвращает true если связка старая (без owner_id).
        IsCrosspostOwnerByTgChat(tgChatID, userID int64) bool

        // ClearMessagesMapping удаляет все записи из таблицы messages для указанного tgChatID.
        ClearMessagesMapping(tgChatID int64) error

        // SetCrosspostLiveListen включает или выключает "живое прослушивание" новых постов.
        SetCrosspostLiveListen(maxChatID int64, liveListen bool) bool

        // GetCrosspostLiveListen возвращает текущее состояние live_listen для связки.
        GetCrosspostLiveListen(maxChatID int64) bool

        // CreateSyncTask создаёт новую запись sync_task для ретроспективного скачивания.
        CreateSyncTask(task SyncTask) (int64, error)

        // Users
        TouchUser(userID int64, platform, username, firstName string)
        ListUsers(platform string) ([]int64, error)

        // Send queue (retry при недоступности MAX/TG API)
        EnqueueSend(item *QueueItem) error
        PeekQueue(limit int) ([]QueueItem, error)
        DeleteFromQueue(id int64) error
        IncrementAttempt(id int64, nextRetry int64) error

        // Sync tasks (ретроспективная синхронизация TG→MAX)
        GetPendingSyncTasks() ([]SyncTask, error)
        SetSyncTaskStatus(id int64, status, errMsg string) error
        UpdateSyncTaskLastID(id int64, lastSyncedID string) error
        DeleteCompletedSyncTasks(userID int64) error

        // Sprint 4: профиль пользователя и список задач для Mini App
        GetUserProfile(userID int64) (*UserProfile, error)
        ListUserSyncTasks(userID int64) ([]SyncTask, error)

        // MAX known chats (чаты, в которых состоит MAX-бот)
        UpsertMaxKnownChat(chat MaxKnownChat)
        ListMaxKnownChats() []MaxKnownChat

        // MTProto SaaS Sessions
        GetMTProtoSession(userID int64) ([]byte, error)
        SaveMTProtoSession(userID int64, sessionData []byte) error

        // Управление подписками
        GrantSubscription(userID int64, days int) error

        Close() error
}

// UserProfile — данные профиля пользователя для Mini App.
type UserProfile struct {
        UserID          int64      `json:"user_id"`
        Platform        string     `json:"platform"`
        Username        string     `json:"username"`
        FirstName       string     `json:"first_name"`
        SubscriptionEnd *time.Time `json:"subscription_end,omitempty"`
        HasSubscription bool       `json:"has_subscription"`
        AdminContact    string     `json:"admin_contact,omitempty"`
}

// SyncTask — задача ретроспективного скачивания постов из TG-канала в MAX.
type SyncTask struct {
        ID           int64
        UserID       int64
        TgChatID     int64
        MaxChatID    int64
        Status       string // pending | processing | done | failed
        StartDate    time.Time
        EndDate      time.Time
        LastSyncedID string
        Error        string
}

// MaxKnownChat — MAX-чат, в котором состоит бот.
type MaxKnownChat struct {
        ChatID   int64  `json:"chat_id"`
        Title    string `json:"title"`
        ChatType string `json:"chat_type"`
}

// QueueItem — сообщение в очереди на повторную отправку.
type QueueItem struct {
        ID        int64
        Direction string // "tg2max" or "max2tg"
        SrcChatID int64
        DstChatID int64
        SrcMsgID  string // TG msg ID (as string) or MAX mid
        Text      string
        AttType   string // "video", "file", "audio", ""
        AttToken  string
        ReplyTo   string
        Format    string
        AttURL    string // URL медиа (для MAX→TG)
        ParseMode string // "HTML" или ""
        Attempts  int
        CreatedAt int64
        NextRetry int64
}
