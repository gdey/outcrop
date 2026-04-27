package migrations

import (
	"database/sql"
	"embed"

	"github.com/gdey/goose/v3"
)

const Dir = "."

var Provider = goose.NewProvider(
	goose.Verbose(false),
	goose.Filesystem(FS),
	goose.BaseDir(""),
	goose.Dialect("sqlite3"),
	goose.Log(quietLogger{}), // suppress "OK <file>" lines per migration
)

//go:embed *.sql
var FS embed.FS

func Up(db *sql.DB, opts ...goose.OptionsFunc) error { return Provider.Up(db, Dir, opts...) }

// quietLogger discards goose's stdout chatter. Migration errors still flow
// back through the goose.Up return value.
type quietLogger struct{}

func (quietLogger) Fatal(v ...any)                 {}
func (quietLogger) Fatalf(format string, v ...any) {}
func (quietLogger) Print(v ...any)                 {}
func (quietLogger) Println(v ...any)               {}
func (quietLogger) Printf(format string, v ...any) {}
