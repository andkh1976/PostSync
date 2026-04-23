package main

import (
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestBuildMaxChannelConfirmationCode(t *testing.T) {
	code := buildMaxChannelConfirmationCode()
	matched, err := regexp.MatchString(`^MAX-[A-F0-9]{6}$`, code)
	if err != nil {
		t.Fatalf("regex error: %v", err)
	}
	if !matched {
		t.Fatalf("unexpected confirmation code format: %s", code)
	}
}

func TestParseMaxChannelConfirmationCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantCode string
		wantChat int64
		wantOK   bool
	}{
		{name: "valid", input: "/confirm MAX-1A2B3C 123456", wantCode: "MAX-1A2B3C", wantChat: 123456, wantOK: true},
		{name: "valid with extra spaces", input: " /confirm   max-1a2b3c   987654 ", wantCode: "MAX-1A2B3C", wantChat: 987654, wantOK: true},
		{name: "missing chat id", input: "/confirm MAX-1A2B3C", wantOK: false},
		{name: "bad chat id", input: "/confirm MAX-1A2B3C nope", wantOK: false},
		{name: "wrong command", input: "/start MAX-1A2B3C 123", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCode, gotChat, gotOK := parseMaxChannelConfirmationCommand(tt.input)
			if gotOK != tt.wantOK {
				t.Fatalf("ok mismatch: got %v want %v", gotOK, tt.wantOK)
			}
			if gotCode != tt.wantCode {
				t.Fatalf("code mismatch: got %q want %q", gotCode, tt.wantCode)
			}
			if gotChat != tt.wantChat {
				t.Fatalf("chat mismatch: got %d want %d", gotChat, tt.wantChat)
			}
		})
	}
}

func TestMaxChannelConfirmationCanBeUsed(t *testing.T) {
	now := time.Date(2026, 4, 23, 15, 0, 0, 0, time.UTC)

	confirmed := MaxChannelConfirmation{
		Status:    MaxChannelConfirmationStatusConfirmed,
		ExpiresAt: now.Add(10 * time.Minute),
	}
	if !confirmed.CanBeUsed(now) {
		t.Fatal("confirmed non-expired confirmation should be usable")
	}

	expired := MaxChannelConfirmation{
		Status:    MaxChannelConfirmationStatusConfirmed,
		ExpiresAt: now.Add(-1 * time.Minute),
	}
	if expired.CanBeUsed(now) {
		t.Fatal("expired confirmation must not be usable")
	}

	pending := MaxChannelConfirmation{
		Status:    MaxChannelConfirmationStatusPending,
		ExpiresAt: now.Add(10 * time.Minute),
	}
	if pending.CanBeUsed(now) {
		t.Fatal("pending confirmation must not be usable")
	}

	used := MaxChannelConfirmation{
		Status:    MaxChannelConfirmationStatusUsed,
		ExpiresAt: now.Add(10 * time.Minute),
	}
	if used.CanBeUsed(now) {
		t.Fatal("used confirmation must not be reusable")
	}
}

func TestSQLiteMaxChannelConfirmationLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repoAny, err := NewSQLiteRepo(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteRepo failed: %v", err)
	}
	defer repoAny.Close()

	repo, ok := repoAny.(*sqliteRepo)
	if !ok {
		t.Fatalf("unexpected repo type %T", repoAny)
	}

	now := time.Date(2026, 4, 23, 16, 0, 0, 0, time.UTC)
	created, err := repo.CreateMaxChannelConfirmation(MaxChannelConfirmation{
		TgUserID:  101,
		MaxChatID: 202,
		Code:      "MAX-ABC123",
		Status:    MaxChannelConfirmationStatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(15 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateMaxChannelConfirmation failed: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected created confirmation id")
	}

	confirmed, err := repo.MarkMaxChannelConfirmationConfirmed(created.Code, created.MaxChatID, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("MarkMaxChannelConfirmationConfirmed failed: %v", err)
	}
	if confirmed.Status != MaxChannelConfirmationStatusConfirmed {
		t.Fatalf("expected confirmed status, got %s", confirmed.Status)
	}
	if confirmed.ConfirmedAt == nil {
		t.Fatal("expected confirmed_at to be set")
	}

	usable, err := repo.GetUsableMaxChannelConfirmation(created.TgUserID, created.MaxChatID, now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("GetUsableMaxChannelConfirmation failed: %v", err)
	}
	if usable.ID != created.ID {
		t.Fatalf("expected usable confirmation id %d, got %d", created.ID, usable.ID)
	}

	if err := repo.MarkMaxChannelConfirmationUsed(created.ID, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("MarkMaxChannelConfirmationUsed failed: %v", err)
	}
	used, err := repo.GetMaxChannelConfirmationByCode(created.Code)
	if err != nil {
		t.Fatalf("GetMaxChannelConfirmationByCode after use failed: %v", err)
	}
	if used.Status != MaxChannelConfirmationStatusUsed {
		t.Fatalf("expected used status, got %s", used.Status)
	}

	expiring, err := repo.CreateMaxChannelConfirmation(MaxChannelConfirmation{
		TgUserID:  101,
		MaxChatID: 303,
		Code:      "MAX-DEF456",
		Status:    MaxChannelConfirmationStatusPending,
		CreatedAt: now,
		ExpiresAt: now.Add(1 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Create expiring confirmation failed: %v", err)
	}
	if err := repo.ExpireMaxChannelConfirmations(now.Add(2 * time.Minute)); err != nil {
		t.Fatalf("ExpireMaxChannelConfirmations failed: %v", err)
	}
	expired, err := repo.GetMaxChannelConfirmationByCode(expiring.Code)
	if err != nil {
		t.Fatalf("Get expired confirmation failed: %v", err)
	}
	if expired.Status != MaxChannelConfirmationStatusExpired {
		t.Fatalf("expected expired status, got %s", expired.Status)
	}
}
