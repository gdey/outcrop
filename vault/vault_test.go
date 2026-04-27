package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRelPath(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		valid bool
	}{
		{"empty", "", false},
		{"simple", "Clippings", true},
		{"nested", "Clippings/attachments", true},
		{"absolute unix", "/etc/passwd", false},
		{"absolute via dot", "..", false},
		{"escape segment", "Clippings/../../etc", false},
		{"hidden escape segment", "a/b/../../../etc", false},
		{"trailing dotdot", "Clippings/..", false},
		{"leading dotdot", "../etc", false},
		{"windows drive", `C:\foo`, false},
		{"dot is fine", "a/./b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRelPath(tt.in)
			if tt.valid && err != nil {
				t.Fatalf("expected valid, got %v", err)
			}
			if !tt.valid && err == nil {
				t.Fatalf("expected invalid, got nil")
			}
		})
	}
}

func TestWriteExclusiveAndAtomic(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "out.txt")

	if err := WriteExclusive(abs, []byte("first")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	// Second exclusive write must fail.
	err := WriteExclusive(abs, []byte("second"))
	if err == nil || !os.IsExist(err) {
		t.Fatalf("expected ErrExist, got %v", err)
	}

	// Atomic write should succeed and overwrite.
	if err := WriteAtomic(abs, []byte("replaced")); err != nil {
		t.Fatalf("atomic write: %v", err)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "replaced" {
		t.Fatalf("contents = %q, want %q", got, "replaced")
	}
}

func TestEnsureDirs(t *testing.T) {
	root := t.TempDir()
	v := Vault{Path: root, ClippingPath: "Clippings", AttachmentPath: "Clippings/attachments"}
	if err := v.EnsureDirs(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	for _, sub := range []string{"Clippings", "Clippings/attachments"} {
		info, err := os.Stat(filepath.Join(root, sub))
		if err != nil {
			t.Fatalf("stat %s: %v", sub, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a dir", sub)
		}
	}

	// Bad path is rejected before any mkdir happens.
	bad := Vault{Path: root, ClippingPath: "../escape", AttachmentPath: "Clippings/attachments"}
	if err := bad.EnsureDirs(); err == nil {
		t.Fatalf("expected error on escaping path")
	}
}
