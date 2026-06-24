package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

// newStartCmd runs the daemon until interrupted.
func newStartCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Run the backup daemon in the foreground",
		Long: "Start scans the project tree, mirrors discovered .env files into the\n" +
			"backup repository, watches them for changes, and commits & pushes on a\n" +
			"schedule. It runs until it receives SIGINT or SIGTERM.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, _, _, err := f.build(cmd)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return d.Run(ctx)
		},
	}
}

// newScanCmd performs a single scan/mirror pass without touching git.
func newScanCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "scan",
		Short: "Scan for .env files and refresh the manifest (no commit)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, log, _, err := f.build(cmd)
			if err != nil {
				return err
			}

			log.Infof("Scanning projects...")
			if err := d.Reconcile(context.Background(), nil); err != nil {
				return err
			}
			log.Infof("Tracking %d env files.", d.Manifest().Len())
			return nil
		},
	}
}

// newSyncCmd performs a scan then commits & pushes any changes immediately.
func newSyncCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Scan, then commit & push pending changes now",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, log, _, err := f.build(cmd)
			if err != nil {
				return err
			}

			ctx := context.Background()
			log.Infof("Scanning projects...")
			if err := d.Reconcile(ctx, nil); err != nil {
				return err
			}
			log.Infof("Running git sync...")
			return d.SyncGit(ctx)
		},
	}
}

// newStatusCmd prints the current tracked-file state. It never prints secret
// values — only paths, hashes and timestamps.
func newStatusCmd(f *flags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show tracked files and their sync state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d, _, cfg, err := f.build(cmd)
			if err != nil {
				return err
			}

			entries := d.Manifest().Entries()
			fmt.Printf("Scan root:  %s\n", cfg.ScanRoot)
			fmt.Printf("Repo path:  %s\n", cfg.RepoPath)
			fmt.Printf("Tracked:    %d file(s)\n\n", len(entries))

			if len(entries) == 0 {
				fmt.Println("No files tracked yet. Run `env-sync scan`.")
				return nil
			}

			tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			fmt.Fprintln(tw, "BACKUP\tHASH\tDIRTY\tLAST SYNC")
			for _, e := range entries {
				hash := e.SHA256
				if len(hash) > 12 {
					hash = hash[:12]
				}
				last := "never"
				if !e.LastSync.IsZero() {
					last = e.LastSync.Format(time.RFC3339)
				}
				dirty := "no"
				if e.Dirty {
					dirty = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", e.Backup, hash, dirty, last)
			}
			return tw.Flush()
		},
	}
}
