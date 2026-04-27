// Package clip orchestrates writing a single clip — a markdown note plus its
// attached PNG — into a vault.
package clip

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdey/outcrop/vault"
)

// ErrInvalidImage indicates the supplied imageBase64 could not be decoded as
// a valid PNG. Callers should treat this as a 4xx-class error.
var ErrInvalidImage = errors.New("invalid image")

// Input is the data the server hands to Write after decoding the JSON body.
type Input struct {
	URL          string
	Title        string
	SelectedText string
	Notes        string
	ImageBase64  string
	When         time.Time // capture timestamp; defaults to time.Now().UTC()
}

// Result names the files written, both as paths relative to the vault root.
type Result struct {
	NotePath  string // e.g. "Clippings/clip-20260426T143200-471.md"
	ImagePath string // e.g. "Clippings/attachments/clip-20260426T143200-471.png"
}

// Write writes a clip into the vault. It creates the destination directories
// if needed, picks a filename, decodes and writes the PNG with O_EXCL, then
// writes the markdown note. On filename collision it bumps the millisecond
// component up to a small retry budget.
func Write(v vault.Vault, in Input) (Result, error) {
	if in.When.IsZero() {
		in.When = time.Now().UTC()
	} else {
		in.When = in.When.UTC()
	}

	imgBytes, err := decodeAndValidatePNG(in.ImageBase64)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidImage, err)
	}

	if err := v.EnsureDirs(); err != nil {
		return Result{}, err
	}

	const retries = 1000 // covers up to 1s of millisecond collisions
	when := in.When
	for i := 0; i < retries; i++ {
		base := fmt.Sprintf("clip-%s-%03d",
			when.Format("20060102T150405"),
			when.Nanosecond()/int(time.Millisecond))
		imgRel := filepath.Join(v.AttachmentPath, base+".png")
		noteRel := filepath.Join(v.ClippingPath, base+".md")

		imgAbs, err := v.Resolve(imgRel)
		if err != nil {
			return Result{}, err
		}

		if err := vault.WriteExclusive(imgAbs, imgBytes); err != nil {
			if errors.Is(err, os.ErrExist) {
				when = when.Add(time.Millisecond)
				continue
			}
			return Result{}, err
		}

		noteAbs, err := v.Resolve(noteRel)
		if err != nil {
			_ = os.Remove(imgAbs)
			return Result{}, err
		}

		body := composeNote(in, base+".png")
		if err := vault.WriteAtomic(noteAbs, []byte(body)); err != nil {
			_ = os.Remove(imgAbs)
			return Result{}, fmt.Errorf("write note: %w", err)
		}

		return Result{
			NotePath:  filepath.ToSlash(noteRel),
			ImagePath: filepath.ToSlash(imgRel),
		}, nil
	}

	return Result{}, fmt.Errorf("filename collision after %d retries", retries)
}

func decodeAndValidatePNG(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, errors.New("imageBase64 is empty")
	}
	// Tolerate a "data:image/png;base64," prefix if the extension sends one.
	if i := strings.Index(b64, "base64,"); i >= 0 {
		b64 = b64[i+len("base64,"):]
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("not a valid PNG: %w", err)
	}
	return data, nil
}

func composeNote(in Input, imageName string) string {
	var b strings.Builder

	titleLink := in.Title
	if titleLink == "" {
		titleLink = in.URL
	}
	fmt.Fprintf(&b, "# Clipped from [%s](%s)\n\n", titleLink, in.URL)
	fmt.Fprintf(&b, "*Captured %s*\n\n", in.When.Format("2006-01-02 15:04 UTC"))
	fmt.Fprintf(&b, "![[%s]]\n", imageName)

	if strings.TrimSpace(in.Notes) != "" {
		fmt.Fprintf(&b, "\n## Notes\n\n%s\n", strings.TrimRight(in.Notes, "\n"))
	}
	if strings.TrimSpace(in.SelectedText) != "" {
		fmt.Fprintf(&b, "\n## Selected Text\n\n")
		for _, line := range strings.Split(strings.TrimRight(in.SelectedText, "\n"), "\n") {
			fmt.Fprintf(&b, "> %s\n", line)
		}
	}
	return b.String()
}
