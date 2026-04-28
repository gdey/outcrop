package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdey/outcrop/store"
)

// CmdTrainingData implements `outcrop training-data <subcommand>`. RFD 0011
// scoped capture for v1; `clear` is the only mutation tool — date-filtered
// `prune` and export tooling stay deferred until there's enough captured
// data to make them worth building.
func CmdTrainingData(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: outcrop training-data <enable|disable|status|clear>")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "enable":
		return cmdTrainingEnable(rest)
	case "disable":
		return cmdTrainingDisable(rest)
	case "status":
		return cmdTrainingStatus(rest)
	case "clear":
		return cmdTrainingClear(rest)
	default:
		return fmt.Errorf("unknown training-data subcommand %q", sub)
	}
}

func cmdTrainingEnable(args []string) error {
	fs := flag.NewFlagSet("training-data enable", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	fmt.Fprintln(os.Stdout, "Enabling training-data capture (RFD 0011).")
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Each successful clip will record one row to the local SQLite DB:")
	fmt.Fprintln(os.Stdout, "  - timestamp")
	fmt.Fprintln(os.Stdout, "  - URL, title, selected text, your notes")
	fmt.Fprintln(os.Stdout, "  - the candidate vault list (with descriptions) at capture time")
	fmt.Fprintln(os.Stdout, "  - the vault you actually picked")
	fmt.Fprintln(os.Stdout, "  - paths to the saved markdown note and the cropped image")
	fmt.Fprintln(os.Stdout, "")
	fmt.Fprintln(os.Stdout, "Stored locally only — outcrop never uploads anything. Disable any time")
	fmt.Fprintln(os.Stdout, "with `outcrop training-data disable`.")
	fmt.Fprintln(os.Stdout, "")

	if err := st.SetMeta(context.Background(), store.MetaTrainingDataEnabled, "true"); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Enabled. Capture starts on the next successful POST /clip.")
	return nil
}

func cmdTrainingDisable(args []string) error {
	fs := flag.NewFlagSet("training-data disable", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.SetMeta(context.Background(), store.MetaTrainingDataEnabled, "false"); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Disabled. Existing rows are preserved; new clips will not be captured.")
	return nil
}

func cmdTrainingStatus(args []string) error {
	fs := flag.NewFlagSet("training-data status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	enabled, err := st.Meta(ctx, store.MetaTrainingDataEnabled)
	if err != nil {
		return err
	}
	count, err := st.TrainingExampleCount(ctx)
	if err != nil {
		return err
	}
	suggested, overrides, err := st.TrainingSuggestionStats(ctx)
	if err != nil {
		return err
	}
	last, hasLast, err := st.LastTrainingExampleTime(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stdout, "Enabled:          %v\n", enabled == "true")
	fmt.Fprintf(os.Stdout, "Captured rows:    %d\n", count)
	fmt.Fprintf(os.Stdout, "With suggestion:  %d%s\n", suggested, percentOf(suggested, count))
	fmt.Fprintf(os.Stdout, "Overrides:        %d%s\n", overrides, percentOf(overrides, suggested))
	if hasLast {
		fmt.Fprintf(os.Stdout, "Most recent:      %s\n", last.Local().Format(time.RFC3339))
	} else {
		fmt.Fprintln(os.Stdout, "Most recent:      (none)")
	}
	return nil
}

// percentOf returns " (P%)" for n out of total, or "" when total is zero so
// the line stays clean on a fresh DB.
func percentOf(n, total int) string {
	if total <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%d%%)", int(float64(n)/float64(total)*100+0.5))
}

func cmdTrainingClear(args []string) error {
	fs := flag.NewFlagSet("training-data clear", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	if err := fs.Parse(args); err != nil {
		return err
	}

	st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	count, err := st.TrainingExampleCount(ctx)
	if err != nil {
		return err
	}
	if count == 0 {
		fmt.Fprintln(os.Stdout, "No training-data rows to clear.")
		return nil
	}

	if !*yes {
		if !stdinIsTTY() {
			return fmt.Errorf("would clear %d training-data rows; pass --yes to confirm in non-interactive contexts", count)
		}
		fmt.Fprintf(os.Stdout, "This will permanently delete %d training-data rows. This cannot be undone.\nProceed? [y/N] ", count)
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
		default:
			return errors.New("aborted")
		}
	}

	n, err := st.ClearTrainingExamples(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "Cleared %d training-data rows.\n", n)
	return nil
}
