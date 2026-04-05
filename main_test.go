package main

import (
	"os"
	"regexp"
	"testing"
)

func TestEnvOrConfiguration(t *testing.T) {
	testData := []struct {
		envKey      string
		valueStr    string
		fallbackStr string
		finalAns    string
	}{
		{"SAMPLE_A_SET", "hello", "world", "hello"},
		{"SAMPLE_B_EMPTY", "", "world", "world"},
	}

	for idx, entry := range testData {
		name := string(rune('A' + idx))
		t.Run("env_"+name, func(t *testing.T) {
			if len(entry.valueStr) > 0 {
				os.Setenv(entry.envKey, entry.valueStr)
				defer os.Unsetenv(entry.envKey)
			} else {
				os.Unsetenv(entry.envKey)
			}

			result := envOr(entry.envKey, entry.fallbackStr)
			if result != entry.finalAns {
				t.Fatalf("Failed EnvOr check: expected %s, got %s", entry.finalAns, result)
			}
		})
	}
}

func TestGenKeyAlgorithm(t *testing.T) {
	k1 := genKey()

	// Length boundary verification
	if l := len(k1); l != 16 {
		t.Fatalf("Invalid genKey length: %d", l)
	}

	k2 := genKey()
	// Weak uniqueness detection
	if k1 == k2 {
		t.Fatalf("collision detected in genKey: %s", k1)
	}

	// Character boundary verification formatted gracefully
	matched, err := regexp.MatchString(`^[a-f0-9]+$`, k1)
	if err != nil || !matched {
		t.Fatalf("genKey produced invalid hex string representation: %s", k1)
	}
}
