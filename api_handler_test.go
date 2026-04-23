package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestBridge(t *testing.T) (*Bridge, *sqliteRepo) {
	t.Helper()
	repoAny, err := NewSQLiteRepo(t.TempDir() + `\handler-test.db`)
	if err != nil {
		t.Fatalf("NewSQLiteRepo failed: %v", err)
	}
	repo, ok := repoAny.(*sqliteRepo)
	if !ok {
		t.Fatalf("unexpected repo type %T", repoAny)
	}
	b := &Bridge{repo: repo}
	b.authMiddlewareFunc = func(r *http.Request) (*apiOwner, error) {
		return &apiOwner{UserID: 101, Platform: "tg"}, nil
	}
	b.checkAccessFunc = func(userID int64) bool { return true }
	b.checkTgAdminFunc = func(chatID, userID int64) (bool, error) { return true, nil }
	return b, repo
}

func TestHandleAPIMaxChannelConfirmations(t *testing.T) {
	b, repo := newTestBridge(t)
	defer repo.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/max-channel/confirmations", strings.NewReader(`{"max_chat_id":202}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	b.handleAPIMaxChannelConfirmations(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Code         string `json:"code"`
		Status       string `json:"status"`
		MaxChatID    int64  `json:"max_chat_id"`
		Instructions string `json:"instructions"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json decode failed: %v", err)
	}
	if body.Status != MaxChannelConfirmationStatusPending {
		t.Fatalf("expected pending status, got %s", body.Status)
	}
	if body.MaxChatID != 202 {
		t.Fatalf("expected max_chat_id 202, got %d", body.MaxChatID)
	}
	if body.Code == "" || !strings.Contains(body.Instructions, body.Code) {
		t.Fatalf("expected code and instructions with code, got body=%+v", body)
	}

	stored, err := repo.GetMaxChannelConfirmationByCode(body.Code)
	if err != nil {
		t.Fatalf("confirmation not stored: %v", err)
	}
	if stored.TgUserID != 101 {
		t.Fatalf("expected tg user 101, got %d", stored.TgUserID)
	}
}

func TestHandleAPIChannelsPairRequiresConfirmedMaxChannel(t *testing.T) {
	b, repo := newTestBridge(t)
	defer repo.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/channels/pair", strings.NewReader(`{"tg_chat_id":-1001,"max_chat_id":202}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	b.handleAPIChannelsPair(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Сначала подтвердите владение MAX-каналом") {
		t.Fatalf("unexpected body: %s", rr.Body.String())
	}
}

func TestHandleAPIChannelsPairUsesConfirmedMaxChannel(t *testing.T) {
	b, repo := newTestBridge(t)
	defer repo.Close()

	now := time.Now().UTC()
	created, err := repo.CreateMaxChannelConfirmation(MaxChannelConfirmation{
		TgUserID:  101,
		MaxChatID: 202,
		Code:      "MAX-PAIR01",
		Status:    MaxChannelConfirmationStatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateMaxChannelConfirmation failed: %v", err)
	}
	if _, err := repo.MarkMaxChannelConfirmationConfirmed(created.Code, created.MaxChatID, now.Add(1*time.Minute)); err != nil {
		t.Fatalf("MarkMaxChannelConfirmationConfirmed failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/channels/pair", strings.NewReader(`{"tg_chat_id":-1001,"max_chat_id":202}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	b.handleAPIChannelsPair(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	maxChatID, dir, ok := repo.GetCrosspostMaxChat(-1001, 101)
	if !ok {
		t.Fatal("expected crosspost pair to be stored")
	}
	if maxChatID != 202 {
		t.Fatalf("expected max chat 202, got %d", maxChatID)
	}
	if dir != "both" {
		t.Fatalf("expected direction both, got %s", dir)
	}
	used, err := repo.GetMaxChannelConfirmationByCode(created.Code)
	if err != nil {
		t.Fatalf("GetMaxChannelConfirmationByCode failed: %v", err)
	}
	if used.Status != MaxChannelConfirmationStatusUsed {
		t.Fatalf("expected used confirmation, got %s", used.Status)
	}
}
