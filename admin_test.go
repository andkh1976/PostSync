package main

import (
	"testing"
	maxschemes "github.com/max-messenger/max-bot-api-client-go/schemes"
)

func TestTgGroupDetection(t *testing.T) {
	cases := map[string]bool{
		"group":      true,
		"supergroup": true,
		"private":    false,
		"channel":    false,
		"":           false,
		"invalid":    false, // added edge case
	}
	for val, expected := range cases {
		t.Run("check_tggroup_"+val, func(t *testing.T) {
			if res := isTgGroup(val); res != expected {
				t.Fatalf("isTgGroup(%v) should be %v", val, expected)
			}
		})
	}
}

func TestTgAdminCheckValidation(t *testing.T) {
	var evaluations = []struct {
		input  string
		result bool
	}{
		{input: "administrator", result: true},
		{input: "creator", result: true},
		{input: "member", result: false},
		{input: "kicked", result: false},
		{input: "left", result: false},
		{input: "restricted", result: false},
		{input: "owner", result: false},
		{input: "", result: false},
	}
	for idx := range evaluations {
		tc := evaluations[idx]
		t.Run("adminstat_"+tc.input, func(t *testing.T) {
			ans := isTgAdmin(tc.input)
			if ans != tc.result {
				t.Errorf("Error parsing tg admin: intput %q gave %v", tc.input, ans)
			}
		})
	}
}

func TestMaxGroupTypeLogic(t *testing.T) {
	scenarios := []struct {
		mType maxschemes.ChatType
		req   bool
	}{
		{maxschemes.CHAT, true},
		{maxschemes.CHANNEL, true},
		{maxschemes.DIALOG, false},
		{maxschemes.ChatType("unknown"), false},
		{"", false},
	}
	for _, s := range scenarios {
		t.Run("maxgroup_"+string(s.mType), func(t *testing.T) {
			if isMaxGroup(s.mType) != s.req {
				t.Errorf("Mismatch for max group type %q", s.mType)
			}
		})
	}
}

func TestMaxUserAdminSearch(t *testing.T) {
	testParticipants := []maxschemes.ChatMember{
		{UserId: 10, Name: "A", IsOwner: true, IsAdmin: true},
		{UserId: 20, Name: "B", IsAdmin: true},
		{UserId: 30, Name: "C", IsBot: true, IsAdmin: true},
	}

	checks := []struct {
		id  int64
		exp bool
	}{
		{10, true},
		{20, true},
		{30, true},
		{40, false},
		{0, false},
		{-1, false},
	}

	for _, c := range checks {
		t.Run("uidcheck", func(t *testing.T) {
			if isMaxUserAdmin(testParticipants, c.id) != c.exp {
				t.Fatalf("Validation failed for id %d", c.id)
			}
		})
	}
}

func TestMaxUserAdminSearch_NilSlices(t *testing.T) {
    // Ensuring specific slice handling behaves identically
    resOne := isMaxUserAdmin(nil, 50)
    if resOne {
        t.Fatal("nil array traversal should fail gracefully")
    }
    
    emptySlice := make([]maxschemes.ChatMember, 0)
    if isMaxUserAdmin(emptySlice, 50) {
        t.Fatal("empty array traversal should fail gracefully")
    }
}

func TestTgChannelCategorization(t *testing.T) {
	checkVals := map[string]bool{
		"channel":    true,
		"group":      false,
		"supergroup": false,
		"private":    false,
		"":           false,
		"bot":        false, // edge check
	}
	for k, v := range checkVals {
		t.Run("chan_eval_"+k, func(t *testing.T) {
			if isTgChannel(k) != v {
				t.Errorf("Expected %v for channel val %s", v, k)
			}
		})
	}
}
