package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte(`
# comment
  CHAEBOT_T_PLAIN = value with spaces
CHAEBOT_T_DQ="double"
CHAEBOT_T_SQ='single'
CHAEBOT_T_MIXED="mixed'
CHAEBOT_T_INNER=say "hi"
CHAEBOT_T_EQ=a=b
CHAEBOT_T_SET=from file
`), 0o644)
	t.Setenv("CHAEBOT_T_SET", "from env")
	for _, k := range []string{"PLAIN", "DQ", "SQ", "MIXED", "INNER", "EQ"} {
		t.Setenv("CHAEBOT_T_"+k, "") // registers cleanup
		os.Unsetenv("CHAEBOT_T_" + k)
	}
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"PLAIN": "value with spaces",
		"DQ":    "double",
		"SQ":    "single",
		"MIXED": `"mixed'`,
		"INNER": `say "hi"`,
		"EQ":    "a=b",
		"SET":   "from env",
	} {
		if got := os.Getenv("CHAEBOT_T_" + k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestLoadDotEnvErrors(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("missing file: %v", err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte("OK=1\nnot a pair\n"), 0o644)
	t.Setenv("OK", "")
	if err := loadDotEnv(path); err == nil || err.Error() != path+":2: expected KEY=VALUE" {
		t.Errorf("bad line: err = %v", err)
	}
}

func TestTwitchAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twitch_token.json")
	set := func(token, clientID, refresh string) {
		t.Setenv("CHAEBOT_TWITCH_TOKEN", token)
		t.Setenv("CHAEBOT_TWITCH_CLIENT_ID", clientID)
		t.Setenv("CHAEBOT_TWITCH_REFRESH_TOKEN", refresh)
	}

	set("", "", "")
	if _, _, err := twitchAuth(path); err == nil {
		t.Error("no token and no refresh: want error")
	}
	set("tok", "id", "")
	if _, _, err := twitchAuth(path); err == nil {
		t.Error("client ID without refresh token: want error")
	}
	set("tok", "", "")
	if tok, refresh, err := twitchAuth(path); err != nil || tok != "tok" || refresh != nil {
		t.Errorf("token only: %q, refresh=%v, %v", tok, refresh != nil, err)
	}

	set("", "id", "seed")
	if tok, refresh, err := twitchAuth(path); err != nil || tok != "" || refresh == nil {
		t.Errorf("refresh only: %q, refresh=%v, %v", tok, refresh != nil, err)
	}

	// A token saved from the same seed is preferred over .env...
	if err := saveTokens(path, savedTokens{Seed: "seed", Access: "newer", Refresh: "rotated"}); err != nil {
		t.Fatal(err)
	}
	set("stale", "id", "seed")
	if tok, _, _ := twitchAuth(path); tok != "newer" {
		t.Errorf("saved token ignored: got %q", tok)
	}
	// ...but a new refresh token pasted into .env replaces it.
	set("pasted", "id", "other-seed")
	if tok, _, _ := twitchAuth(path); tok != "pasted" {
		t.Errorf("new .env refresh token ignored: got %q", tok)
	}
}
