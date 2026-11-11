package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFromEnvTokenPrecedence(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "admin.token"), []byte("admin-tok\n"), 0600)
	file := filepath.Join(t.TempDir(), "token")
	os.WriteFile(file, []byte("  file-tok \n"), 0600)
	t.Setenv("AGORA_HOME", home)
	t.Setenv("HOME", t.TempDir())
	cases := []struct {
		name, env, file, want string
	}{
		{"env wins", " env-tok\n", file, "env-tok"},
		{"file when env empty", "", file, "file-tok"},
		{"admin fallback", "", "", "admin-tok"},
		{"missing file does not fall back to admin", "", filepath.Join(home, "missing"), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("AGORA_TOKEN", c.env)
			t.Setenv("AGORA_TOKEN_FILE", c.file)
			if got := FromEnv().Token; got != c.want {
				t.Fatalf("token %q, want %q", got, c.want)
			}
		})
	}
}
