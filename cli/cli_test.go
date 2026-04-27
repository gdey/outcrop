package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdey/outcrop/store"
)

// setupCLI redirects DBPath() into a tempdir via the OUTCROP_DB override and
// returns the resolved DB path. t.Setenv ensures the env var is restored.
func setupCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "outcrop.db")
	t.Setenv("OUTCROP_DB", dbPath)
	return dbPath
}

// captureStdout runs fn with os.Stdout redirected to a pipe, returning what
// fn wrote and the error fn returned.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	runErr := fn()
	_ = w.Close()
	os.Stdout = orig
	<-done
	return buf.String(), runErr
}

// initForTest sets up an OUTCROP_DB path and runs CmdInit so subsequent vault
// and config subcommands have a populated DB to work against.
func initForTest(t *testing.T) string {
	t.Helper()
	dbPath := setupCLI(t)
	if _, err := captureStdout(t, func() error { return CmdInit(nil) }); err != nil {
		t.Fatalf("init: %v", err)
	}
	return dbPath
}

func TestCmdInit_FreshConfig(t *testing.T) {
	dbPath := setupCLI(t)

	out, err := captureStdout(t, func() error { return CmdInit(nil) })
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("db not created: %v", err)
	}
	for _, want := range []string{"Token", "Listen address", "outcrop vault add"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	tok, _ := st.Meta(context.Background(), store.MetaToken)
	// 32 random bytes → 43 base64url chars (no padding).
	if len(tok) < 32 {
		t.Errorf("token too short: %q", tok)
	}
	addr, _ := st.Meta(context.Background(), store.MetaListenAddr)
	if addr != "127.0.0.1:7878" {
		t.Errorf("listen_addr = %q, want default", addr)
	}
}

func TestCmdInit_RefusesIfExists(t *testing.T) {
	setupCLI(t)
	if _, err := captureStdout(t, func() error { return CmdInit(nil) }); err != nil {
		t.Fatalf("first init: %v", err)
	}
	_, err := captureStdout(t, func() error { return CmdInit(nil) })
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected already-exists error, got %v", err)
	}
}

func TestCmdInit_ForceRotatesToken(t *testing.T) {
	dbPath := setupCLI(t)
	if _, err := captureStdout(t, func() error { return CmdInit(nil) }); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(dbPath)
	before, _ := st.Meta(context.Background(), store.MetaToken)
	_ = st.Close()

	if _, err := captureStdout(t, func() error { return CmdInit([]string{"--force"}) }); err != nil {
		t.Fatalf("force init: %v", err)
	}
	st, _ = store.Open(dbPath)
	after, _ := st.Meta(context.Background(), store.MetaToken)
	_ = st.Close()

	if before == "" || after == "" {
		t.Fatalf("empty token (before=%q after=%q)", before, after)
	}
	if before == after {
		t.Errorf("--force did not rotate the token")
	}
}

func TestVaultAdd_BecomesDefaultIfFirst(t *testing.T) {
	dbPath := initForTest(t)
	d := t.TempDir()

	out, err := captureStdout(t, func() error { return CmdVault([]string{"add", "Personal", d}) })
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "(default)") {
		t.Errorf("first vault should announce default; output:\n%s", out)
	}

	st, _ := store.Open(dbPath)
	defer st.Close()
	list, _ := st.ListVaults(context.Background())
	if len(list) != 1 {
		t.Fatalf("len = %d", len(list))
	}
	def, _ := st.Meta(context.Background(), store.MetaDefaultVaultKey)
	if def != list[0].Key {
		t.Errorf("default = %q, want %q", def, list[0].Key)
	}
}

func TestVaultAdd_SecondVaultDoesNotChangeDefault(t *testing.T) {
	dbPath := initForTest(t)
	d1, d2 := t.TempDir(), t.TempDir()
	mustAdd(t, "First", d1)
	mustAdd(t, "Second", d2)

	st, _ := store.Open(dbPath)
	defer st.Close()
	def, _ := st.Meta(context.Background(), store.MetaDefaultVaultKey)
	if got := nameForKey(t, st, def); got != "First" {
		t.Errorf("default vault = %q, want First (was set on first add)", got)
	}
}

func TestVaultAdd_DefaultFlagSetsDefault(t *testing.T) {
	dbPath := initForTest(t)
	d1, d2 := t.TempDir(), t.TempDir()
	mustAdd(t, "First", d1)
	if _, err := captureStdout(t, func() error {
		return CmdVault([]string{"add", "--default", "Second", d2})
	}); err != nil {
		t.Fatal(err)
	}

	st, _ := store.Open(dbPath)
	defer st.Close()
	def, _ := st.Meta(context.Background(), store.MetaDefaultVaultKey)
	if got := nameForKey(t, st, def); got != "Second" {
		t.Errorf("default = %q, want Second", got)
	}
}

func TestVaultAdd_RejectsMissingPath(t *testing.T) {
	initForTest(t)
	_, err := captureStdout(t, func() error {
		return CmdVault([]string{"add", "Bogus", "/nonexistent/path/here-XYZ"})
	})
	if err == nil {
		t.Errorf("expected error for missing path")
	}
}

func TestVaultAdd_RejectsFile(t *testing.T) {
	initForTest(t)
	f := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := captureStdout(t, func() error {
		return CmdVault([]string{"add", "Bogus", f})
	})
	if err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("expected not-a-directory, got %v", err)
	}
}

func TestVaultList_Empty(t *testing.T) {
	initForTest(t)
	out, err := captureStdout(t, func() error { return CmdVault([]string{"list"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no vaults configured") {
		t.Errorf("output:\n%s", out)
	}
}

func TestVaultList_PrintsRows(t *testing.T) {
	initForTest(t)
	mustAdd(t, "Personal", t.TempDir())
	out, err := captureStdout(t, func() error { return CmdVault([]string{"list"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Personal") || !strings.Contains(out, "KEY") {
		t.Errorf("output:\n%s", out)
	}
}

func TestVaultRename(t *testing.T) {
	dbPath := initForTest(t)
	mustAdd(t, "Old", t.TempDir())
	key := firstVaultKey(t, dbPath)

	if _, err := captureStdout(t, func() error {
		return CmdVault([]string{"rename", key, "New"})
	}); err != nil {
		t.Fatalf("rename: %v", err)
	}

	st, _ := store.Open(dbPath)
	defer st.Close()
	v, _ := st.GetVault(context.Background(), key)
	if v.DisplayName != "New" {
		t.Errorf("name = %q, want New", v.DisplayName)
	}
}

func TestVaultRename_UnknownKey(t *testing.T) {
	initForTest(t)
	_, err := captureStdout(t, func() error { return CmdVault([]string{"rename", "nope", "x"}) })
	if err == nil || !strings.Contains(err.Error(), "no vault with key") {
		t.Errorf("got %v", err)
	}
}

func TestVaultRemove_ClearsDefault(t *testing.T) {
	dbPath := initForTest(t)
	mustAdd(t, "Solo", t.TempDir())
	key := firstVaultKey(t, dbPath)

	out, err := captureStdout(t, func() error { return CmdVault([]string{"remove", key}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "default cleared") {
		t.Errorf("output:\n%s", out)
	}

	st, _ := store.Open(dbPath)
	defer st.Close()
	def, _ := st.Meta(context.Background(), store.MetaDefaultVaultKey)
	if def != "" {
		t.Errorf("default = %q, want empty", def)
	}
}

func TestVaultRemove_UnknownKey(t *testing.T) {
	initForTest(t)
	_, err := captureStdout(t, func() error { return CmdVault([]string{"remove", "nope"}) })
	if err == nil || !strings.Contains(err.Error(), "no vault with key") {
		t.Errorf("got %v", err)
	}
}

func TestVaultDefault(t *testing.T) {
	dbPath := initForTest(t)
	mustAdd(t, "First", t.TempDir())
	mustAdd(t, "Second", t.TempDir())

	st, _ := store.Open(dbPath)
	list, _ := st.ListVaults(context.Background())
	st.Close()
	var secondKey string
	for _, v := range list {
		if v.DisplayName == "Second" {
			secondKey = v.Key
		}
	}
	if secondKey == "" {
		t.Fatal("Second not found in list")
	}

	if _, err := captureStdout(t, func() error {
		return CmdVault([]string{"default", secondKey})
	}); err != nil {
		t.Fatalf("default: %v", err)
	}

	st, _ = store.Open(dbPath)
	defer st.Close()
	def, _ := st.Meta(context.Background(), store.MetaDefaultVaultKey)
	if def != secondKey {
		t.Errorf("default = %q, want %q", def, secondKey)
	}
}

func TestVaultDefault_UnknownKey(t *testing.T) {
	initForTest(t)
	_, err := captureStdout(t, func() error { return CmdVault([]string{"default", "nope"}) })
	if err == nil || !strings.Contains(err.Error(), "no vault with key") {
		t.Errorf("got %v", err)
	}
}

func TestVaultList_NoConfig(t *testing.T) {
	setupCLI(t) // intentionally NOT initialised
	_, err := captureStdout(t, func() error { return CmdVault([]string{"list"}) })
	if err == nil || !strings.Contains(err.Error(), "outcrop init") {
		t.Errorf("expected init-required error, got %v", err)
	}
}

func TestConfigShow_RedactsToken(t *testing.T) {
	initForTest(t)
	out, err := captureStdout(t, func() error { return CmdConfig([]string{"show"}) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(set; pass --show-token to reveal)") {
		t.Errorf("expected redacted token marker, got:\n%s", out)
	}
}

func TestConfigShow_RevealsToken(t *testing.T) {
	dbPath := initForTest(t)
	st, _ := store.Open(dbPath)
	tok, _ := st.Meta(context.Background(), store.MetaToken)
	st.Close()

	out, err := captureStdout(t, func() error { return CmdConfig([]string{"show", "--show-token"}) })
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" || !strings.Contains(out, tok) {
		t.Errorf("token not revealed in output:\n%s", out)
	}
}

func TestConfigPath(t *testing.T) {
	dbPath := setupCLI(t)
	out, err := captureStdout(t, func() error { return CmdConfig([]string{"path"}) })
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != dbPath {
		t.Errorf("path = %q, want %q", got, dbPath)
	}
}

func TestUnknownConfigSubcommand(t *testing.T) {
	_, err := captureStdout(t, func() error { return CmdConfig([]string{"weird"}) })
	if err == nil {
		t.Error("expected error for unknown config subcommand")
	}
}

func TestUnknownVaultSubcommand(t *testing.T) {
	initForTest(t)
	_, err := captureStdout(t, func() error { return CmdVault([]string{"weird"}) })
	if err == nil {
		t.Error("expected error for unknown vault subcommand")
	}
}

// mustAdd adds a vault via the CLI and fails the test on error.
func mustAdd(t *testing.T, name, path string) {
	t.Helper()
	if _, err := captureStdout(t, func() error {
		return CmdVault([]string{"add", name, path})
	}); err != nil {
		t.Fatalf("add %q: %v", name, err)
	}
}

// firstVaultKey returns the key of the first row in vaults (alphabetical).
func firstVaultKey(t *testing.T, dbPath string) string {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	list, err := st.ListVaults(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("no vaults")
	}
	return list[0].Key
}

func nameForKey(t *testing.T, st *store.Store, key string) string {
	t.Helper()
	v, err := st.GetVault(context.Background(), key)
	if err != nil {
		t.Fatalf("get %q: %v", key, err)
	}
	return v.DisplayName
}
