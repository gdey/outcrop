package clip

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdey/outcrop/vault"
)

// tinyPNG returns a 1x1 PNG as base64.
func tinyPNG(t *testing.T) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestWrite_HappyPath(t *testing.T) {
	root := t.TempDir()
	v := vault.Vault{Path: root, ClippingPath: "Clippings", AttachmentPath: "Clippings/attachments"}
	in := Input{
		URL:          "https://example.com/article",
		Title:        "Example Article",
		Notes:        "my notes",
		SelectedText: "quoted bit",
		ImageBase64:  tinyPNG(t),
		When:         time.Date(2026, 4, 26, 14, 32, 0, 471_000_000, time.UTC),
	}
	res, err := Write(v, in)
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if !strings.HasPrefix(res.NotePath, "Clippings/clip-20260426T143200-471") {
		t.Errorf("note path = %q", res.NotePath)
	}
	if !strings.HasPrefix(res.ImagePath, "Clippings/attachments/clip-20260426T143200-471") {
		t.Errorf("image path = %q", res.ImagePath)
	}

	body, err := os.ReadFile(filepath.Join(root, res.NotePath))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	for _, want := range []string{
		"# Clipped from [Example Article](https://example.com/article)",
		"![[clip-20260426T143200-471.png]]",
		"## Notes",
		"my notes",
		"## Selected Text",
		"> quoted bit",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("note missing %q\n--- body ---\n%s", want, body)
		}
	}

	if _, err := os.Stat(filepath.Join(root, res.ImagePath)); err != nil {
		t.Errorf("image not present: %v", err)
	}
}

func TestWrite_InvalidImage(t *testing.T) {
	root := t.TempDir()
	v := vault.Vault{Path: root, ClippingPath: "Clippings", AttachmentPath: "Clippings/attachments"}
	_, err := Write(v, Input{
		URL:         "https://example.com/",
		ImageBase64: "not base64!!!",
	})
	if err == nil || !errors.Is(err, ErrInvalidImage) {
		t.Fatalf("got %v, want ErrInvalidImage", err)
	}
}

func TestWrite_OmitsEmptySections(t *testing.T) {
	root := t.TempDir()
	v := vault.Vault{Path: root, ClippingPath: "Clippings", AttachmentPath: "Clippings/attachments"}
	in := Input{
		URL:         "https://example.com/article",
		Title:       "Just a title",
		ImageBase64: tinyPNG(t),
	}
	res, err := Write(v, in)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(root, res.NotePath))
	for _, unwanted := range []string{"## Notes", "## Selected Text"} {
		if strings.Contains(string(body), unwanted) {
			t.Errorf("body unexpectedly contains %q\n%s", unwanted, body)
		}
	}
}
