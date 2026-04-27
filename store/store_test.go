package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "outcrop.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestOpen_FreshDB_RunsMigrations(t *testing.T) {
	st := openTestStore(t)

	// All three tables should exist (migration has run).
	for _, table := range []string{"meta", "vaults", "history"} {
		var name string
		err := st.DB().QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %s missing: %v", table, err)
		}
	}
}

func TestMeta_RoundTrip(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Missing key returns "" with no error.
	got, err := st.Meta(ctx, "nonexistent")
	if err != nil || got != "" {
		t.Errorf("missing key: got %q, err %v", got, err)
	}

	if err := st.SetMeta(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	got, err = st.Meta(ctx, "k")
	if err != nil || got != "v1" {
		t.Errorf("after set: got %q, err %v", got, err)
	}

	// Upsert.
	if err := st.SetMeta(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Meta(ctx, "k")
	if got != "v2" {
		t.Errorf("after upsert: %q", got)
	}

	// Delete.
	if err := st.DeleteMeta(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Meta(ctx, "k")
	if got != "" {
		t.Errorf("after delete: %q", got)
	}
}

func TestVaultCRUD(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	v := Vault{Key: "k1", DisplayName: "Personal", Path: "/tmp/p"}
	if err := st.CreateVault(ctx, v); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := st.GetVault(ctx, "k1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.DisplayName != "Personal" || got.Path != "/tmp/p" {
		t.Errorf("got %+v", got)
	}
	if got.ClippingPath != "Clippings" || got.AttachmentPath != "Clippings/attachments" {
		t.Errorf("defaults not applied: %+v", got)
	}

	// List sorts by display name.
	if err := st.CreateVault(ctx, Vault{Key: "k2", DisplayName: "Archive", Path: "/tmp/a"}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListVaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].DisplayName != "Archive" || list[1].DisplayName != "Personal" {
		t.Errorf("list order: %+v", list)
	}

	// Rename.
	if err := st.RenameVault(ctx, "k1", "Renamed"); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetVault(ctx, "k1")
	if got.DisplayName != "Renamed" {
		t.Errorf("rename: %s", got.DisplayName)
	}

	// Delete.
	if err := st.DeleteVault(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetVault(ctx, "k1"); !errors.Is(err, ErrVaultNotFound) {
		t.Errorf("expected ErrVaultNotFound, got %v", err)
	}

	// Operations on missing keys return ErrVaultNotFound.
	if err := st.RenameVault(ctx, "nope", "x"); !errors.Is(err, ErrVaultNotFound) {
		t.Errorf("rename missing: %v", err)
	}
	if err := st.DeleteVault(ctx, "nope"); !errors.Is(err, ErrVaultNotFound) {
		t.Errorf("delete missing: %v", err)
	}
}

func TestHistory_RecordAndRank(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for _, v := range []Vault{
		{Key: "kA", DisplayName: "A", Path: "/tmp/a"},
		{Key: "kB", DisplayName: "B", Path: "/tmp/b"},
		{Key: "kC", DisplayName: "C", Path: "/tmp/c"},
	} {
		if err := st.CreateVault(ctx, v); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 4, 26, 14, 0, 0, 0, time.UTC)
	// Three clips to example.com: kB then kA then kC. Most recent should win.
	if err := st.RecordClip(ctx, "example.com", "kB", now.Add(0)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordClip(ctx, "example.com", "kA", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordClip(ctx, "example.com", "kC", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Bump kB to make it the most-recent.
	if err := st.RecordClip(ctx, "example.com", "kB", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	keys, err := st.VaultKeysForDomain(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kB", "kC", "kA"}
	if len(keys) != len(want) {
		t.Fatalf("len = %d, want %d (%v)", len(keys), len(want), keys)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("keys[%d] = %s, want %s", i, keys[i], want[i])
		}
	}

	// Unknown domain returns no rows, no error.
	keys, err = st.VaultKeysForDomain(ctx, "unknown.example")
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("unknown domain should be empty, got %v", keys)
	}
}

func TestHistory_CascadesOnVaultDelete(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.CreateVault(ctx, Vault{Key: "kA", DisplayName: "A", Path: "/tmp/a"}); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordClip(ctx, "example.com", "kA", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// Confirm history exists.
	keys, _ := st.VaultKeysForDomain(ctx, "example.com")
	if len(keys) != 1 {
		t.Fatalf("expected 1 history row, got %d", len(keys))
	}

	// Delete the vault; FK cascade should drop the history row.
	if err := st.DeleteVault(ctx, "kA"); err != nil {
		t.Fatal(err)
	}
	keys, _ = st.VaultKeysForDomain(ctx, "example.com")
	if len(keys) != 0 {
		t.Errorf("FK cascade did not drop history: %v", keys)
	}
}

func TestOpen_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "outcrop.db")

	st1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st1.SetMeta(context.Background(), "k", "v"); err != nil {
		t.Fatal(err)
	}
	st1.Close()

	// Re-open. Migrations should be no-op; existing data preserved.
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	got, _ := st2.Meta(context.Background(), "k")
	if got != "v" {
		t.Errorf("data not preserved: got %q", got)
	}
}
