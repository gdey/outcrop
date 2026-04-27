package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdey/outcrop/store"
)

func tinyPNGBase64(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// newClipTestServer wires a *Server to a fresh test store with one registered
// vault directory on disk. Returns the server and the inserted vault row.
func newClipTestServer(t *testing.T) (*Server, store.Vault) {
	t.Helper()
	dbDir := t.TempDir()
	st, err := store.Open(filepath.Join(dbDir, "outcrop.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	v := store.Vault{
		Key:            "kClipTest",
		DisplayName:    "Test",
		Path:           t.TempDir(),
		ClippingPath:   "Clippings",
		AttachmentPath: "Clippings/attachments",
	}
	if err := st.CreateVault(context.Background(), v); err != nil {
		t.Fatalf("create vault: %v", err)
	}

	s := &Server{store: st, log: discardLogger(), token: "tok", addr: "127.0.0.1:0"}
	return s, v
}

func postClip(t *testing.T, s *Server, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/clip", bytes.NewReader(raw))
	w := httptest.NewRecorder()
	s.handleClip(w, r)
	return w
}

func TestHandleClip_HappyPath(t *testing.T) {
	s, v := newClipTestServer(t)

	w := postClip(t, s, map[string]any{
		"vault":        v.Key,
		"url":          "https://example.com/article",
		"title":        "Example",
		"notes":        "n",
		"selectedText": "s",
		"imageBase64":  tinyPNGBase64(t),
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
	}
	var resp clipResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.HasPrefix(resp.NotePath, "Clippings/clip-") || !strings.HasSuffix(resp.NotePath, ".md") {
		t.Errorf("notePath = %q", resp.NotePath)
	}
	if !strings.HasPrefix(resp.ImagePath, "Clippings/attachments/clip-") || !strings.HasSuffix(resp.ImagePath, ".png") {
		t.Errorf("imagePath = %q", resp.ImagePath)
	}
	if _, err := os.Stat(filepath.Join(v.Path, resp.NotePath)); err != nil {
		t.Errorf("note not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(v.Path, resp.ImagePath)); err != nil {
		t.Errorf("image not written: %v", err)
	}

	keys, err := s.store.VaultKeysForDomain(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(keys) != 1 || keys[0] != v.Key {
		t.Errorf("history = %v, want [%s]", keys, v.Key)
	}
}

func TestHandleClip_MalformedBody(t *testing.T) {
	s, _ := newClipTestServer(t)

	r := httptest.NewRequest(http.MethodPost, "/clip", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	s.handleClip(w, r)

	assertErrorCode(t, w, http.StatusBadRequest, "bad_request")
}

func TestHandleClip_DisallowsUnknownFields(t *testing.T) {
	s, v := newClipTestServer(t)
	w := postClip(t, s, map[string]any{
		"vault":       v.Key,
		"imageBase64": tinyPNGBase64(t),
		"extra":       "junk",
	})
	assertErrorCode(t, w, http.StatusBadRequest, "bad_request")
}

func TestHandleClip_MissingVaultKey(t *testing.T) {
	s, _ := newClipTestServer(t)
	w := postClip(t, s, map[string]any{
		"imageBase64": tinyPNGBase64(t),
	})
	assertErrorCode(t, w, http.StatusBadRequest, "bad_request")
}

func TestHandleClip_UnknownVault(t *testing.T) {
	s, _ := newClipTestServer(t)
	w := postClip(t, s, map[string]any{
		"vault":       "nonexistent",
		"imageBase64": tinyPNGBase64(t),
	})
	assertErrorCode(t, w, http.StatusBadRequest, "vault_not_found")
}

func TestHandleClip_BadImage(t *testing.T) {
	s, v := newClipTestServer(t)
	w := postClip(t, s, map[string]any{
		"vault":       v.Key,
		"imageBase64": "not!!base64",
	})
	assertErrorCode(t, w, http.StatusBadRequest, "image_decode_failed")
}

// When the URL is absent, the request still succeeds but no history row is
// recorded (registrableDomain returns "" → handler skips the upsert).
func TestHandleClip_NoURLSkipsHistory(t *testing.T) {
	s, v := newClipTestServer(t)
	w := postClip(t, s, map[string]any{
		"vault":       v.Key,
		"imageBase64": tinyPNGBase64(t),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", w.Code, w.Body.String())
	}
	var count int
	err := s.store.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM history`).Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("history rows = %d, want 0", count)
	}
}

func assertErrorCode(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, wantStatus, w.Body.String())
	}
	var er errorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &er); err != nil {
		t.Fatalf("unmarshal error body: %v (body: %s)", err, w.Body.String())
	}
	if er.Error != wantCode {
		t.Errorf("error code = %q, want %q (message: %q)", er.Error, wantCode, er.Message)
	}
}
