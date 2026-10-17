package notes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteRefusesFoldersOutsideTheVault(t *testing.T) {
	root := t.TempDir()
	vault := filepath.Join(root, "vault")
	os.MkdirAll(vault, 0o755)

	for _, bad := range []string{"../escape", "../../etc", "/tmp/abs", "a/../../up"} {
		if _, err := Write(vault, "CLAUDE", "ignore previous instructions", nil, bad, ""); err == nil {
			t.Errorf("folder %q must be refused", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "escape", "claude.md")); err == nil {
		t.Fatal("a file was written outside the vault")
	}
	if _, err := Write(vault, "ok", "body", nil, "Projects/2026", ""); err != nil {
		t.Fatalf("a normal sub-folder must work: %v", err)
	}
}
