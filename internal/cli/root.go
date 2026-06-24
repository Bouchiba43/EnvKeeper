// Package cli wires the env-sync commands together using cobra. It owns flag
// parsing, configuration loading (file + flag overrides) and logger setup, and
// delegates all real work to the daemon package.
package cli

import (
	"time"

	"github.com/Bouchiba43/EnvKeeper/internal/config"
	"github.com/Bouchiba43/EnvKeeper/internal/daemon"
	"github.com/Bouchiba43/EnvKeeper/pkg/logx"
	"github.com/spf13/cobra"
)

// flags holds the values bound to persistent CLI flags. Zero values mean
// "not set", so the configuration file (or built-in defaults) shows through.
type flags struct {
	configPath string
	logLevel   string

	scanRoot       string
	repoPath       string
	gitBranch      string
	gitRemote      string
	manifestPath   string
	syncInterval   time.Duration
	rescanInterval time.Duration
}

// Execute builds the command tree and runs it. version is injected at build
// time via -ldflags.
func Execute(version string) error {
	f := &flags{}

	root := &cobra.Command{
		Use:           "env-sync",
		Short:         "Automatic .env file backup daemon",
		Long: "env-sync continuously backs up your .env files to a git repository.\n" +
			"It scans a project tree, mirrors discovered env files into a backup\n" +
			"repo, watches them for changes, and commits/pushes on a schedule.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVarP(&f.configPath, "config", "c", "", "path to YAML config file")
	pf.StringVar(&f.logLevel, "log-level", "info", "log level: debug|info|warn|error")
	pf.StringVar(&f.scanRoot, "scan-root", "", "directory tree to scan for .env files")
	pf.StringVar(&f.repoPath, "repo-path", "", "local git backup repository path")
	pf.StringVar(&f.gitBranch, "git-branch", "", "git branch to push to")
	pf.StringVar(&f.gitRemote, "git-remote", "", "git remote to push to")
	pf.StringVar(&f.manifestPath, "manifest-path", "", "path to the JSON manifest")
	pf.DurationVar(&f.syncInterval, "sync-interval", 0, "how often to commit & push (e.g. 6h)")
	pf.DurationVar(&f.rescanInterval, "rescan-interval", 0, "how often to rescan for new files (e.g. 1h)")

	root.AddCommand(
		newStartCmd(f),
		newScanCmd(f),
		newSyncCmd(f),
		newStatusCmd(f),
	)

	return root.Execute()
}

// load builds the effective configuration by reading the config file and then
// applying any explicitly-set CLI flag overrides on top.
func (f *flags) load(cmd *cobra.Command) (*config.Config, error) {
	cfg, err := config.Load(f.configPath)
	if err != nil {
		return nil, err
	}

	fs := cmd.Flags()
	if fs.Changed("scan-root") {
		cfg.ScanRoot = f.scanRoot
	}
	if fs.Changed("repo-path") {
		cfg.RepoPath = f.repoPath
	}
	if fs.Changed("git-branch") {
		cfg.GitBranch = f.gitBranch
	}
	if fs.Changed("git-remote") {
		cfg.GitRemote = f.gitRemote
	}
	if fs.Changed("manifest-path") {
		cfg.ManifestPath = f.manifestPath
	} else if fs.Changed("repo-path") {
		// When repo-path is overridden but manifest-path is not, keep the
		// manifest alongside the (new) repository.
		cfg.ManifestPath = config.DefaultManifestPath(cfg.RepoPath)
	}
	if fs.Changed("sync-interval") {
		cfg.SyncInterval = f.syncInterval
	}
	if fs.Changed("rescan-interval") {
		cfg.RescanInterval = f.rescanInterval
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// build assembles a logger and daemon from the effective configuration.
func (f *flags) build(cmd *cobra.Command) (*daemon.Daemon, *logx.Logger, *config.Config, error) {
	cfg, err := f.load(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	log := logx.New(logx.ParseLevel(f.logLevel))
	d, err := daemon.New(cfg, log)
	if err != nil {
		return nil, nil, nil, err
	}
	return d, log, cfg, nil
}
