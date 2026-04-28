package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/tools/libs"
)

// EnsureKronkLibs installs kronk's llama.cpp shared libraries at kronk's
// expected location (~/.kronk/libraries/<os>/<arch>/<processor>/) and
// validates that kronk.Init() succeeds against them.
//
// Idempotent — when libs are already present at the right version for the
// detected (os, arch, processor) triple, the underlying Download is a no-op
// and this function returns quickly.
//
// log receives one line per kronk progress event (download start/finish,
// version info, extraction). Pass nil to discard.
func EnsureKronkLibs(ctx context.Context, log func(msg string)) error {
	lib, err := libs.New()
	if err != nil {
		return fmt.Errorf("resolve kronk libs path: %w", err)
	}

	libsLog := libs.Logger(func(_ context.Context, msg string, args ...any) {
		if log == nil {
			return
		}
		var b strings.Builder
		b.WriteString(msg)
		for i := 0; i+1 < len(args); i += 2 {
			fmt.Fprintf(&b, " %v=%v", args[i], args[i+1])
		}
		log(b.String())
	})

	if _, err := lib.Download(ctx, libsLog); err != nil {
		return fmt.Errorf("download kronk libs: %w", err)
	}

	if err := kronk.Init(); err != nil {
		return fmt.Errorf("kronk init after lib download: %w", err)
	}
	return nil
}

// ProbeKronk attempts to initialize the kronk backend (dlopens libllama).
// Returns nil if the libs are present and loadable. Idempotent: subsequent
// calls in the same process are no-ops once Init has succeeded.
//
// Has side effects: kronk caches the loaded library handle for the process
// lifetime, and may set PATH / LD_LIBRARY_PATH so dlopen finds the lib.
// Safe in one-shot CLI invocations; the server already calls Init via the
// KronkSuggester, so an extra status probe is harmless.
func ProbeKronk() error {
	return kronk.Init()
}
