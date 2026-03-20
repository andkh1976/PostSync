# MAX Antispam Bot Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an antispam bot for MAX messenger with local filters, AI classification (premium), captcha, flood detection, and configurable punishment escalation.

**Architecture:** Flat Go package (like bridge). MAX Bot API for messaging. Two-tier moderation: fast local filter scores every message, only suspicious ones go to AI (premium). SQLite default, PostgreSQL optional. Config via bot DM with inline keyboards.

**Tech Stack:** Go 1.24, MAX Bot API SDK (`max-bot-api-client-go`), SQLite/PostgreSQL, OpenRouter API, `golang-migrate/migrate/v4`

**Project:** `/home/bearlogin/development/bearlogin/max-antispam`

---

## File Structure

```
max-antispam/
  main.go              — entry point, env config, wiring
  bot.go               — Bot struct, Run(), MAX polling loop, callback handler
  repository.go        — Repository interface
  sqlite.go            — SQLite implementation
  postgres.go          — PostgreSQL implementation
  migrate.go           — migration runner (same pattern as bridge)
  migrations/
    sqlite/
      000001_init.up.sql
      000001_init.down.sql
    postgres/
      000001_init.up.sql
      000001_init.down.sql
  filter.go            — local filter engine (stopwords, regex, links, unicode)
  score.go             — suspicion scoring system
  flood.go             — flood detection (rate limiter per user/chat)
  captcha.go           — new member verification (button + timeout)
  punish.go            — punishment escalation engine
  ai.go                — OpenRouter AI classifier (premium)
  settings.go          — inline keyboard settings UI in bot DM
  premium.go           — premium key management
  deploy.sh            — deploy script (same pattern as bridge)
  Makefile             — build/run/test
  go.mod
  LICENSE
  README.md

  filter_test.go
  score_test.go
  flood_test.go
  captcha_test.go
  punish_test.go
  ai_test.go
  settings_test.go
```

---

## Chunk 1: Project Scaffold + Storage

### Task 1: Initialize Go module and project structure

**Files:**
- Create: `go.mod`, `main.go`, `Makefile`, `LICENSE`, `deploy.sh`

- [ ] **Step 1: Create project directory and init module**

```bash
mkdir -p /home/bearlogin/development/bearlogin/max-antispam
cd /home/bearlogin/development/bearlogin/max-antispam
go mod init max-antispam
```

- [ ] **Step 2: Create main.go with env loading and basic wiring**

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "os/signal"
    "strconv"
    "syscall"

    maxbot "github.com/max-messenger/max-bot-api-client-go"
)

func mustEnv(key string) string {
    v := os.Getenv(key)
    if v == "" {
        fmt.Fprintf(os.Stderr, "env %s is required\n", key)
        os.Exit(1)
    }
    return v
}

func envOr(key, fallback string) string {
    if v := os.Getenv(key); v != "" {
        return v
    }
    return fallback
}

func envInt(key string, fallback int) int {
    v := os.Getenv(key)
    if v == "" {
        return fallback
    }
    n, err := strconv.Atoi(v)
    if err != nil {
        return fallback
    }
    return n
}

type Config struct {
    MaxToken       string
    OpenRouterKey  string // empty = AI disabled
    OpenRouterModel string
    FreeChatLimit  int
}

func main() {
    slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

    cfg := Config{
        MaxToken:        mustEnv("MAX_TOKEN"),
        OpenRouterKey:   os.Getenv("OPENROUTER_KEY"),
        OpenRouterModel: envOr("OPENROUTER_MODEL", "openai/gpt-4o-mini"),
        FreeChatLimit:   envInt("FREE_CHAT_LIMIT", 3),
    }

    dbPath := envOr("DB_PATH", "antispam.db")

    var repo Repository
    var err error
    if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
        repo, err = NewPostgresRepo(dsn)
        if err != nil {
            slog.Error("PostgreSQL error", "err", err)
            os.Exit(1)
        }
        slog.Info("DB: PostgreSQL")
    } else {
        repo, err = NewSQLiteRepo(dbPath)
        if err != nil {
            slog.Error("SQLite error", "err", err)
            os.Exit(1)
        }
        slog.Info("DB: SQLite", "path", dbPath)
    }
    defer repo.Close()

    maxApi, err := maxbot.New(cfg.MaxToken)
    if err != nil {
        slog.Error("MAX bot error", "err", err)
        os.Exit(1)
    }
    info, err := maxApi.Bots.GetBot(context.Background())
    if err != nil {
        slog.Error("MAX bot info error", "err", err)
        os.Exit(1)
    }
    slog.Info("MAX bot started", "name", info.Name)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigCh
        slog.Info("Shutting down...")
        cancel()
    }()

    bot := NewBot(cfg, repo, maxApi)
    bot.Run(ctx)
}
```

- [ ] **Step 3: Create Makefile**

```makefile
-include .env
export

.PHONY: build run test vet clean

BINARY = max-antispam

build:
	CGO_ENABLED=1 go build -o $(BINARY) .

run: build
	./$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
```

- [ ] **Step 4: Copy LICENSE from bridge, create deploy.sh**

Copy LICENSE from bridge. Create deploy.sh with same pattern but `SERVICE=max-antispam`, `REMOTE_DIR=/opt/max-antispam`, binary name `max-antispam`.

- [ ] **Step 5: Commit**

```bash
git init
git add -A
git commit -m "Initial project scaffold"
```

---

### Task 2: Repository interface and migrations

**Files:**
- Create: `repository.go`, `migrate.go`, `migrations/sqlite/000001_init.up.sql`, `migrations/sqlite/000001_init.down.sql`, `migrations/postgres/000001_init.up.sql`, `migrations/postgres/000001_init.down.sql`

- [ ] **Step 1: Define Repository interface**

```go
// repository.go
package main

// ChatSettings holds per-chat configuration.
type ChatSettings struct {
    ChatID           int64
    CaptchaEnabled   bool
    CaptchaTimeout   int    // seconds
    FilterEnabled    bool
    FloodEnabled     bool
    FloodMaxMessages int    // max messages per window
    FloodWindowSec   int    // window in seconds
    AIEnabled        bool   // premium only
    AIPrompt         string // custom admin prompt
    ScoreThreshold   int    // suspicion score threshold
    // Punishment chain: "delete", "mute:3600", "ban"
    PunishChain      string // comma-separated actions
    NewUserMessages  int    // how many messages to strictly filter for new users
}

// UserState tracks per-user state in a chat.
type UserState struct {
    ChatID         int64
    UserID         int64
    Verified       bool
    ViolationCount int
    MutedUntil     int64 // unix timestamp, 0 = not muted
    JoinedAt       int64
    MessageCount   int   // messages since joining
}

// Violation is a logged moderation action.
type Violation struct {
    ChatID    int64
    UserID    int64
    Reason    string
    Action    string
    Timestamp int64
}

// Repository abstracts storage for the antispam bot.
type Repository interface {
    // Chat settings
    GetChatSettings(chatID int64) (*ChatSettings, error)
    SaveChatSettings(s *ChatSettings) error
    ListChats() ([]ChatSettings, error)
    DeleteChat(chatID int64) error
    ChatCount() (int, error)

    // Stopwords
    GetStopwords(chatID int64) ([]string, error)
    AddStopword(chatID int64, word string) error
    RemoveStopword(chatID int64, word string) error

    // User state
    GetUserState(chatID, userID int64) (*UserState, error)
    SaveUserState(u *UserState) error
    IncrementViolation(chatID, userID int64) (int, error)

    // Violations log
    LogViolation(v *Violation) error
    GetViolations(chatID int64, limit int) ([]Violation, error)

    // Premium
    IsPremium(chatID int64) bool
    ActivatePremium(chatID int64, key string) error

    Close() error
}
```

- [ ] **Step 2: Create SQLite migration 000001_init**

`migrations/sqlite/000001_init.up.sql`:
```sql
CREATE TABLE IF NOT EXISTS chat_settings (
    chat_id INTEGER PRIMARY KEY,
    captcha_enabled INTEGER NOT NULL DEFAULT 1,
    captcha_timeout INTEGER NOT NULL DEFAULT 60,
    filter_enabled INTEGER NOT NULL DEFAULT 1,
    flood_enabled INTEGER NOT NULL DEFAULT 1,
    flood_max_messages INTEGER NOT NULL DEFAULT 5,
    flood_window_sec INTEGER NOT NULL DEFAULT 10,
    ai_enabled INTEGER NOT NULL DEFAULT 0,
    ai_prompt TEXT NOT NULL DEFAULT '',
    score_threshold INTEGER NOT NULL DEFAULT 8,
    punish_chain TEXT NOT NULL DEFAULT 'delete,mute:3600,ban',
    new_user_messages INTEGER NOT NULL DEFAULT 5
);

CREATE TABLE IF NOT EXISTS stopwords (
    chat_id INTEGER NOT NULL,
    word TEXT NOT NULL,
    PRIMARY KEY (chat_id, word)
);

CREATE TABLE IF NOT EXISTS user_states (
    chat_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    verified INTEGER NOT NULL DEFAULT 0,
    violation_count INTEGER NOT NULL DEFAULT 0,
    muted_until INTEGER NOT NULL DEFAULT 0,
    joined_at INTEGER NOT NULL DEFAULT 0,
    message_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (chat_id, user_id)
);

CREATE TABLE IF NOT EXISTS violations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    reason TEXT NOT NULL,
    action TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS premium (
    chat_id INTEGER PRIMARY KEY,
    key TEXT NOT NULL,
    activated_at INTEGER NOT NULL
);
```

`migrations/sqlite/000001_init.down.sql`:
```sql
DROP TABLE IF EXISTS premium;
DROP TABLE IF EXISTS violations;
DROP TABLE IF EXISTS user_states;
DROP TABLE IF EXISTS stopwords;
DROP TABLE IF EXISTS chat_settings;
```

- [ ] **Step 3: Create PostgreSQL migration 000001_init**

Same schema but with `BIGINT` instead of `INTEGER` for chat/user IDs, `BOOLEAN` instead of `INTEGER` for booleans, `SERIAL` for auto-increment.

- [ ] **Step 4: Create migrate.go**

Copy pattern from bridge's `migrate.go` exactly — embed FS, `runMigrations()`, but without `maybeForceVersion` (fresh project, no legacy).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "Add repository interface and migrations"
```

---

### Task 3: SQLite repository implementation

**Files:**
- Create: `sqlite.go`

- [ ] **Step 1: Implement SQLite repo**

Follow the bridge `sqlite.go` pattern: struct with `db *sql.DB` and `mu sync.Mutex`, implement all Repository methods. Use `INSERT OR REPLACE` for upserts. Each method acquires the mutex.

Key methods:
- `GetChatSettings` — SELECT with defaults if not found (return new ChatSettings with defaults)
- `SaveChatSettings` — INSERT OR REPLACE
- `GetUserState` — SELECT, return new UserState if not found
- `SaveUserState` — INSERT OR REPLACE
- `IncrementViolation` — UPDATE violation_count + 1, return new count
- `IsPremium` — SELECT EXISTS from premium table

- [ ] **Step 2: Commit**

```bash
git add sqlite.go
git commit -m "Implement SQLite repository"
```

---

### Task 4: PostgreSQL repository implementation

**Files:**
- Create: `postgres.go`

- [ ] **Step 1: Implement PostgreSQL repo**

Same as SQLite but using `$1`-style placeholders and `ON CONFLICT ... DO UPDATE` for upserts. Same pattern as bridge's `postgres.go`.

- [ ] **Step 2: Commit**

```bash
git add postgres.go
git commit -m "Implement PostgreSQL repository"
```

---

## Chunk 2: Core Moderation Engine

### Task 5: Suspicion scoring system

**Files:**
- Create: `score.go`, `score_test.go`

- [ ] **Step 1: Write tests for scoring**

```go
// score_test.go
package main

import "testing"

func TestScoreCleanMessage(t *testing.T) {
    s := NewScorer()
    result := s.Score("привет, как дела?", ScoreContext{IsNewUser: false})
    if result.Total > 0 {
        t.Errorf("clean message scored %d, want 0", result.Total)
    }
}

func TestScoreStopword(t *testing.T) {
    s := NewScorer()
    s.SetStopwords([]string{"казино", "крипта"})
    result := s.Score("заходи в казино!", ScoreContext{})
    if result.Total < 3 {
        t.Errorf("stopword message scored %d, want >= 3", result.Total)
    }
    if !result.HasFlag(FlagStopword) {
        t.Error("expected FlagStopword")
    }
}

func TestScoreLinkFromNewUser(t *testing.T) {
    s := NewScorer()
    result := s.Score("зайди на http://spam.com", ScoreContext{IsNewUser: true})
    if result.Total < 5 {
        t.Errorf("link from new user scored %d, want >= 5", result.Total)
    }
}

func TestScoreUnicodeAbuse(t *testing.T) {
    s := NewScorer()
    // zero-width characters
    result := s.Score("п\u200bр\u200bи\u200bв\u200bе\u200bт", ScoreContext{})
    if !result.HasFlag(FlagUnicodeAbuse) {
        t.Error("expected FlagUnicodeAbuse")
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./... -run TestScore -v
```

- [ ] **Step 3: Implement scoring engine**

```go
// score.go
package main

import (
    "regexp"
    "strings"
    "unicode"
)

type Flag int

const (
    FlagStopword     Flag = 1 << iota
    FlagLink
    FlagFlood
    FlagUnicodeAbuse
    FlagNewUserLink
    FlagForward
)

type ScoreResult struct {
    Total int
    Flags Flag
    Details []string
}

func (r *ScoreResult) HasFlag(f Flag) bool {
    return r.Flags&f != 0
}

func (r *ScoreResult) addScore(points int, flag Flag, detail string) {
    r.Total += points
    r.Flags |= flag
    r.Details = append(r.Details, detail)
}

type ScoreContext struct {
    IsNewUser    bool
    MessageCount int // messages since join
}

type Scorer struct {
    stopwords []string
    linkRe    *regexp.Regexp
}

func NewScorer() *Scorer {
    return &Scorer{
        linkRe: regexp.MustCompile(`https?://|t\.me/|max\.ru/`),
    }
}

func (s *Scorer) SetStopwords(words []string) {
    s.stopwords = words
}

func (s *Scorer) Score(text string, ctx ScoreContext) ScoreResult {
    var r ScoreResult
    lower := strings.ToLower(text)

    // Stopwords
    for _, w := range s.stopwords {
        if strings.Contains(lower, strings.ToLower(w)) {
            r.addScore(3, FlagStopword, "stopword: "+w)
        }
    }

    // Links
    if s.linkRe.MatchString(text) {
        if ctx.IsNewUser {
            r.addScore(5, FlagNewUserLink, "link from new user")
        } else {
            r.addScore(1, FlagLink, "link")
        }
    }

    // Unicode abuse (zero-width chars, excessive combining marks)
    zwCount := 0
    for _, ch := range text {
        if ch == '\u200b' || ch == '\u200c' || ch == '\u200d' || ch == '\ufeff' ||
            unicode.Is(unicode.Mn, ch) { // combining marks
            zwCount++
        }
    }
    if zwCount > 3 {
        r.addScore(2, FlagUnicodeAbuse, "unicode abuse")
    }

    return r
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./... -run TestScore -v
```

- [ ] **Step 5: Commit**

```bash
git add score.go score_test.go
git commit -m "Add suspicion scoring engine"
```

---

### Task 6: Local filter engine

**Files:**
- Create: `filter.go`, `filter_test.go`

- [ ] **Step 1: Write tests**

Test cases: clean message passes, stopword caught, regex pattern caught, link from new user caught.

- [ ] **Step 2: Implement filter**

`Filter` struct wraps `Scorer` and chat settings. Main method: `Check(text string, ctx ScoreContext, settings *ChatSettings) FilterResult` returns action (pass/delete/escalate-to-ai) and reason.

Logic:
- If `score >= settings.ScoreThreshold` and chat has AI enabled → `ActionEscalateAI`
- If `score >= settings.ScoreThreshold` and no AI → `ActionDelete`
- If `score > 0 but < threshold` → `ActionPass` (log only)
- If `score == 0` → `ActionPass`

- [ ] **Step 3: Run tests, verify pass**

- [ ] **Step 4: Commit**

```bash
git add filter.go filter_test.go
git commit -m "Add local filter engine"
```

---

### Task 7: Flood detection

**Files:**
- Create: `flood.go`, `flood_test.go`

- [ ] **Step 1: Write tests**

Test: single message passes, N+1 messages within window triggers flood, messages after window expires don't trigger.

- [ ] **Step 2: Implement flood detector**

In-memory sliding window per (chatID, userID). Struct: `FloodDetector` with `sync.Mutex` and `map[key][]time.Time`. Method `Check(chatID, userID int64, maxMsg int, windowSec int) bool` — returns true if flood detected. Periodic cleanup of old entries.

Also detect duplicate messages: track last N message hashes per user. If same hash repeated > 2 times → flood.

- [ ] **Step 3: Run tests, verify pass**

- [ ] **Step 4: Commit**

```bash
git add flood.go flood_test.go
git commit -m "Add flood detection"
```

---

### Task 8: Punishment escalation

**Files:**
- Create: `punish.go`, `punish_test.go`

- [ ] **Step 1: Write tests**

Test: parse chain "delete,mute:3600,ban". 1st violation → delete, 2nd → mute 3600s, 3rd → ban, 4th+ → ban.

- [ ] **Step 2: Implement punisher**

```go
type Action struct {
    Type     string // "delete", "mute", "ban"
    Duration int    // seconds, for mute
}

func ParseChain(chain string) []Action { ... }
func GetAction(chain string, violationCount int) Action { ... }
```

`GetAction` returns the action for the Nth violation. If count exceeds chain length, repeat last action.

- [ ] **Step 3: Run tests, verify pass**

- [ ] **Step 4: Commit**

```bash
git add punish.go punish_test.go
git commit -m "Add punishment escalation"
```

---

## Chunk 3: Captcha + Bot Main Loop

### Task 9: Captcha system

**Files:**
- Create: `captcha.go`, `captcha_test.go`

- [ ] **Step 1: Write tests**

Test: pending captcha created on join, verify callback removes pending, timeout returns expired list.

- [ ] **Step 2: Implement captcha manager**

In-memory map of pending verifications: `map[chatID_userID]pendingCaptcha`. Each has `expiresAt time.Time`. Method `Add(chatID, userID int64, timeout int)`, `Verify(chatID, userID int64) bool`, `Expired() []pending` (called periodically).

The bot sends an inline keyboard with a "I'm not a bot" button. Callback data: `captcha:<chatID>:<userID>`.

- [ ] **Step 3: Run tests, verify pass**

- [ ] **Step 4: Commit**

```bash
git add captcha.go captcha_test.go
git commit -m "Add captcha verification system"
```

---

### Task 10: Bot main loop and message handling

**Files:**
- Create: `bot.go`

- [ ] **Step 1: Create Bot struct and Run()**

```go
type Bot struct {
    cfg      Config
    repo     Repository
    api      *maxbot.Api
    scorer   *Scorer
    flood    *FloodDetector
    captcha  *CaptchaManager
    http     *http.Client
}

func NewBot(cfg Config, repo Repository, api *maxbot.Api) *Bot { ... }
func (b *Bot) Run(ctx context.Context) { ... }
```

`Run()` starts MAX polling. On each update:
- `MessageCreatedUpdate` → `b.handleMessage(ctx, upd)`
- `UserAddedToChatUpdate` → `b.handleJoin(ctx, upd)`
- `CallbackAnswer` → `b.handleCallback(ctx, upd)`

- [ ] **Step 2: Implement handleMessage**

Flow:
1. Ignore bot messages
2. Check if DM → route to settings handler
3. Load chat settings from repo
4. Check if user is muted → delete message
5. If user not verified and captcha enabled → delete message, re-send captcha
6. Run flood check → if triggered, add flood score
7. Run scorer on message text
8. If score >= threshold and AI enabled (premium) → send to AI
9. If score >= threshold (or AI says bad) → execute punishment
10. If user is new (messageCount < newUserMessages) → strict filter (no links/forwards)
11. Otherwise pass

- [ ] **Step 3: Implement handleJoin**

1. Load chat settings
2. If captcha enabled → mute user, send inline button, add to captcha pending
3. If captcha disabled → save user as verified

- [ ] **Step 4: Implement handleCallback**

Route by callback data prefix:
- `captcha:` → verify user, unmute, remove pending
- `settings:` → route to settings handler (Task 11)

- [ ] **Step 5: Periodic tasks**

In `Run()`, start a ticker (every 10s) to:
- Check captcha expired → kick users
- Cleanup flood detector old entries

- [ ] **Step 6: Build and verify compilation**

```bash
go build ./...
```

- [ ] **Step 7: Commit**

```bash
git add bot.go
git commit -m "Add bot main loop with message handling"
```

---

## Chunk 4: Settings UI + AI + Premium

### Task 11: Settings via bot DM (inline keyboards)

**Files:**
- Create: `settings.go`

- [ ] **Step 1: Implement settings handler**

When user sends `/start` in DM:
1. Query MAX API for chats where bot is admin
2. Show list as inline keyboard buttons
3. On chat selected → show settings menu:
   - Captcha: ON/OFF
   - Filters: ON/OFF
   - Flood: ON/OFF
   - AI: ON/OFF (if premium)
   - Punishments: show current chain
   - Stopwords: manage list
4. Each toggle sends callback, handler updates repo and refreshes keyboard

Callback data format: `set:<chatID>:<module>:<value>`

- [ ] **Step 2: Implement stopwords management**

Callback flow: `set:<chatID>:stopwords` → show current words + "Add" button. On "Add" → bot asks for word in next message (store pending state). On word received → add to repo.

- [ ] **Step 3: Commit**

```bash
git add settings.go
git commit -m "Add settings UI via bot DM"
```

---

### Task 12: AI classifier (OpenRouter)

**Files:**
- Create: `ai.go`, `ai_test.go`

- [ ] **Step 1: Write tests**

Mock HTTP transport. Test: clean message → "clean", spam message → "spam". Test custom prompt injection into system message.

- [ ] **Step 2: Implement AI classifier**

```go
type AIClassifier struct {
    apiKey  string
    model   string
    http    *http.Client
}

type AIResult struct {
    Category string  // spam, ads, insult, nsfw, scam, clean
    Confidence float64
    Reason   string
}

func (c *AIClassifier) Classify(ctx context.Context, text string, customPrompt string) (*AIResult, error)
```

OpenRouter API call:
- POST `https://openrouter.ai/api/v1/chat/completions`
- System prompt: built-in classifier instructions + custom admin prompt
- Ask model to respond with JSON: `{"category": "...", "confidence": 0.0-1.0, "reason": "..."}`
- Parse response, return AIResult

Simple fuzzy hash cache: `map[uint64]*AIResult` keyed by FNV hash of normalized text. TTL 1 hour. Prevents re-classifying similar messages.

- [ ] **Step 3: Run tests, verify pass**

- [ ] **Step 4: Commit**

```bash
git add ai.go ai_test.go
git commit -m "Add OpenRouter AI classifier"
```

---

### Task 13: Premium key management

**Files:**
- Create: `premium.go`

- [ ] **Step 1: Implement premium commands**

In bot DM: `/premium <key>` → validate key format, activate in repo for current chat selection. Show "Premium activated" or error.

Keys are pre-generated strings stored in DB. For now, admin generates them manually (INSERT into premium table). Later can add a generation command.

Add helper `(b *Bot) canUseAI(chatID int64) bool` — checks premium + OpenRouter key configured.

- [ ] **Step 2: Add free chat limit check**

In `handleMessage` and `handleJoin`: if chat not in repo and `repo.ChatCount() >= cfg.FreeChatLimit` and not premium → ignore, send one-time message "Free limit reached (3 chats). Activate premium: /premium <key>".

- [ ] **Step 3: Commit**

```bash
git add premium.go
git commit -m "Add premium key management and chat limits"
```

---

## Chunk 5: Deploy + Polish

### Task 14: Integration wiring and deploy

**Files:**
- Modify: `main.go`, `bot.go`
- Create: `deploy.sh`

- [ ] **Step 1: Wire all components in main.go**

Ensure `NewBot()` creates Scorer, FloodDetector, CaptchaManager, AIClassifier (if key provided).

- [ ] **Step 2: Full build + vet + test**

```bash
go build ./...
go vet ./...
go test ./...
```

- [ ] **Step 3: Create deploy.sh**

Same pattern as bridge: build linux/amd64, scp to server, systemd restart. Service name: `max-antispam`, remote dir: `/opt/max-antispam`.

- [ ] **Step 4: Deploy with --setup**

```bash
bash deploy.sh --setup
```

Fill in `.env` on server with `MAX_TOKEN`.

- [ ] **Step 5: Commit and push**

```bash
git add -A
git commit -m "Wire components, add deploy script"
git push origin master
```

---

### Task 15: README

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write README**

Cover: what it does, features (free vs premium), quick start, env vars, commands, deploy, license.

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "Add README"
```
