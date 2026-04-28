package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdey/outcrop/agent"
)

// downloadModel fetches a recommended model into the models dir, verifying
// SHA256 when the recommended entry has one. Returns the absolute path of
// the downloaded file.
//
// Idempotent: if the destination file already exists, it is verified (when
// a SHA256 is known) and returned without re-downloading. --force in the
// CLI bypasses the existing-file shortcut.
func downloadModel(ctx context.Context, m *agent.RecommendedModel, force bool) (string, error) {
	if m == nil {
		return "", errors.New("no model specified")
	}

	dir, err := ModelsDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create models dir: %w", err)
	}

	dst := filepath.Join(dir, m.Filename)

	if !force {
		if info, err := os.Stat(dst); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			if m.SHA256 != "" {
				if err := verifySHA256(dst, m.SHA256); err != nil {
					fmt.Fprintf(os.Stdout, "  existing file at %s failed checksum (%v); re-downloading\n", dst, err)
				} else {
					fmt.Fprintf(os.Stdout, "  already present and verified: %s\n", dst)
					return dst, nil
				}
			} else {
				fmt.Fprintf(os.Stdout, "  already present (skipping checksum — none baked in): %s\n", dst)
				return dst, nil
			}
		}
	}

	partial := dst + ".partial"
	_ = os.Remove(partial) // remove any leftover from a previous failed run

	fmt.Fprintf(os.Stdout, "  downloading %s\n", m.URL)
	if err := downloadStream(ctx, m.URL, partial, m.SizeBytes); err != nil {
		_ = os.Remove(partial)
		return "", fmt.Errorf("download: %w", err)
	}

	if m.SHA256 != "" {
		fmt.Fprintln(os.Stdout, "  verifying checksum…")
		if err := verifySHA256(partial, m.SHA256); err != nil {
			_ = os.Remove(partial)
			return "", fmt.Errorf("verify: %w", err)
		}
	} else {
		fmt.Fprintln(os.Stdout, "  WARNING: no expected SHA256 in recommended-models table; skipping integrity check.")
	}

	if err := os.Rename(partial, dst); err != nil {
		_ = os.Remove(partial)
		return "", fmt.Errorf("rename into place: %w", err)
	}

	fmt.Fprintf(os.Stdout, "  saved: %s\n", dst)
	return dst, nil
}

// downloadStream fetches the URL into dst, printing progress at most once
// per second. Total is the expected byte count for the progress display
// (best-effort; the real Content-Length wins when available).
func downloadStream(ctx context.Context, url, dst string, total int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if cl := resp.ContentLength; cl > 0 {
		total = cl
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	pr := newProgressReader(resp.Body, total)
	if _, err := io.Copy(out, pr); err != nil {
		return err
	}
	pr.flushFinal()
	return out.Sync()
}

// progressReader wraps an io.Reader and prints "X / Y" progress to stdout
// at most twice per second. On a TTY it uses \r so the line is overwritten
// in place; on a pipe it falls back to newline-per-update so log captures
// remain readable. Best-effort UX; not a structured progress bar.
type progressReader struct {
	r        io.Reader
	total    int64
	read     int64
	lastPrnt time.Time
	tty      bool
	// unit is fixed once per reader (picked from total) so the moving and
	// fixed sides of the display use the same scale and stay aligned.
	unit    string
	divisor float64
}

func newProgressReader(r io.Reader, total int64) *progressReader {
	unit, divisor := pickUnit(total)
	return &progressReader{
		r:       r,
		total:   total,
		tty:     stdoutIsTTY(),
		unit:    unit,
		divisor: divisor,
	}
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if time.Since(p.lastPrnt) >= 500*time.Millisecond {
		p.print()
		p.lastPrnt = time.Now()
	}
	return n, err
}

func (p *progressReader) print() {
	prefix := "\r"
	if !p.tty {
		prefix = "\n"
	}
	if p.total > 0 {
		pct := float64(p.read) / float64(p.total) * 100
		fmt.Fprintf(os.Stdout, "%s  %7.3f / %7.3f %s (%6.2f%%)",
			prefix,
			float64(p.read)/p.divisor,
			float64(p.total)/p.divisor,
			p.unit,
			pct,
		)
	} else {
		// Unknown total: just show what we've read so far.
		unit, divisor := pickUnit(p.read)
		fmt.Fprintf(os.Stdout, "%s  %7.3f %s downloaded",
			prefix,
			float64(p.read)/divisor,
			unit,
		)
	}
}

// flushFinal draws the final state and ends the line with a newline so
// subsequent output isn't appended to the progress line.
func (p *progressReader) flushFinal() {
	p.print()
	fmt.Fprintln(os.Stdout)
}

// pickUnit returns the SI-binary unit name suited to n and the divisor that
// converts a byte count to that unit's value as a float64.
func pickUnit(n int64) (string, float64) {
	switch {
	case n >= 1<<30:
		return "GB", float64(1 << 30)
	case n >= 1<<20:
		return "MB", float64(1 << 20)
	case n >= 1<<10:
		return "KB", float64(1 << 10)
	default:
		return "B ", 1
	}
}

func stdoutIsTTY() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func verifySHA256(path, expected string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("sha256 mismatch: got %s, want %s", got, expected)
	}
	return nil
}

func humanBytes(n int64) string {
	const (
		_  = iota
		KB = 1 << (10 * iota)
		MB
		GB
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// cmdAgentDownload implements `outcrop agent download`.
func cmdAgentDownload(args []string) error {
	fs := flag.NewFlagSet("agent download", flag.ContinueOnError)
	id := fs.String("model", "", "recommended model ID (defaults to the first non-vision entry; see --list)")
	vision := fs.Bool("vision", false, "pick the default vision-capable model instead of the default text model")
	force := fs.Bool("force", false, "re-download even if the file is already present")
	list := fs.Bool("list", false, "print the curated recommended-model list and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *list {
		printRecommended()
		return nil
	}

	var m *agent.RecommendedModel
	if *id != "" {
		m = agent.LookupRecommended(*id)
		if m == nil {
			return fmt.Errorf("unknown model %q (try `outcrop agent download --list`)", *id)
		}
	} else {
		m = agent.DefaultRecommended(*vision)
		if m == nil {
			return errors.New("no recommended model available for that selection")
		}
	}

	fmt.Fprintf(os.Stdout, "%s — %s\n  URL:     %s\n  size:    ~%s\n",
		m.DisplayName, m.Tagline, m.URL, humanBytes(m.SizeBytes))

	ctx := context.Background()
	path, err := downloadModel(ctx, m, *force)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "\nDone. Use this file with: outcrop agent enable --backend kronk --model %s\n", path)
	return nil
}

func printRecommended() {
	for _, m := range agent.RecommendedModels {
		visionTag := ""
		if m.Vision {
			visionTag = " [vision]"
		}
		fmt.Fprintf(os.Stdout, "  %s — %s%s\n    %s\n    size: ~%s\n", m.ID, m.DisplayName, visionTag, m.Tagline, humanBytes(m.SizeBytes))
	}
}
