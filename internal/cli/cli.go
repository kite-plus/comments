// Package cli implements the comments command line interface.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/kite-plus/comments/internal/config"
	"github.com/kite-plus/comments/internal/store"
)

// Execute runs the root command. Interrupts cancel the command's context so
// long-running commands can shut down cleanly.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newRootCmd().ExecuteContext(ctx)
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "comments",
		Short: "The Kite Plus comment service",
		Long: "Kite Plus Comments hosts comments for independent blogs, with one\n" +
			"Kite Plus account to comment anywhere the widget is embedded.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolP("json", "j", false, "emit machine readable output")
	root.AddCommand(
		newVersionCmd(),
		newServeCmd(),
		newWorkerCmd(),
		newMigrateCmd(),
		newUsersCmd(),
		newSitesCmd(),
		newDevCmd(),
	)
	return root
}

func loadConfig() (config.Config, error) {
	return config.Load(os.Getenv)
}

// openStore loads the configuration, requires a database and connects.
func openStore(ctx context.Context) (config.Config, *store.Store, *slog.Logger, error) {
	cfg, err := loadConfig()
	if err != nil {
		return cfg, nil, nil, err
	}
	if err := cfg.RequireDatabase(); err != nil {
		return cfg, nil, nil, err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return cfg, nil, nil, err
	}
	return cfg, st, log, nil
}

func jsonOut(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

func printf(cmd *cobra.Command, format string, args ...any) {
	// A failed write to stdout is not something a command can react to.
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), format, args...)
}
