// Package config loads and validates env-sync configuration from a YAML file
// and/or CLI flags. It also defines sensible defaults so the daemon can run
// with zero configuration on a typical setup.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all tunable settings for the daemon. Field tags map to the
// YAML configuration file; durations are parsed from human-friendly strings
// such as "6h" or "1h".
type Config struct {
	// ScanRoot is the directory tree that is recursively scanned for .env files.
	ScanRoot string `yaml:"scan_root"`

	// RepoPath is the local path to the Git backup repository where mirrored
	// files are written and committed.
	RepoPath string `yaml:"repo_path"`

	// SyncInterval is how often the daemon commits and pushes pending changes.
	SyncInterval time.Duration `yaml:"sync_interval"`

	// RescanInterval is how often the daemon rescans ScanRoot to discover new
	// projects and .env files.
	RescanInterval time.Duration `yaml:"rescan_interval"`

	// GitBranch is the branch that changes are pushed to (e.g. "main").
	GitBranch string `yaml:"git_branch"`

	// GitRemote is the remote name that changes are pushed to (e.g. "origin").
	GitRemote string `yaml:"git_remote"`

	// ManifestPath is the location of the JSON manifest tracking file state.
	// When empty it defaults to "<repo_path>/.env-sync-manifest.json".
	ManifestPath string `yaml:"manifest_path"`

	// IgnoreDirs is the set of directory names that are skipped while scanning.
	IgnoreDirs []string `yaml:"ignore_dirs"`

	// FilePatterns is the set of glob patterns used to match env files.
	FilePatterns []string `yaml:"file_patterns"`
}

// Default returns a Config populated with reasonable defaults for a typical
// Fedora workstation setup.
func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		ScanRoot:       filepath.Join(home, "projects"),
		RepoPath:       filepath.Join(home, ".env-sync", "backup"),
		SyncInterval:   6 * time.Hour,
		RescanInterval: 1 * time.Hour,
		GitBranch:      "main",
		GitRemote:      "origin",
		ManifestPath:   "",
		IgnoreDirs:     []string{".git", "node_modules", "vendor", "dist", "build", ".next"},
		FilePatterns:   []string{".env", ".env.*"},
	}
}

// rawConfig mirrors Config but takes string durations so YAML can express them
// in a human-friendly form (e.g. "6h"). A nil duration field means "unset",
// allowing defaults to show through.
type rawConfig struct {
	ScanRoot       string   `yaml:"scan_root"`
	RepoPath       string   `yaml:"repo_path"`
	SyncInterval   string   `yaml:"sync_interval"`
	RescanInterval string   `yaml:"rescan_interval"`
	GitBranch      string   `yaml:"git_branch"`
	GitRemote      string   `yaml:"git_remote"`
	ManifestPath   string   `yaml:"manifest_path"`
	IgnoreDirs     []string `yaml:"ignore_dirs"`
	FilePatterns   []string `yaml:"file_patterns"`
}

// Load reads configuration from the given YAML path, layering any values found
// on top of the defaults. A missing file is not an error: the defaults are
// returned so the daemon can run out of the box.
func Load(path string) (*Config, error) {
	cfg := Default()

	if path == "" {
		return cfg, cfg.finalize()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, cfg.finalize()
		}
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}

	if err := cfg.applyRaw(&raw); err != nil {
		return nil, fmt.Errorf("invalid config %q: %w", path, err)
	}

	return cfg, cfg.finalize()
}

// applyRaw overlays non-empty values from raw onto the receiver.
func (c *Config) applyRaw(raw *rawConfig) error {
	if raw.ScanRoot != "" {
		c.ScanRoot = raw.ScanRoot
	}
	if raw.RepoPath != "" {
		c.RepoPath = raw.RepoPath
	}
	if raw.GitBranch != "" {
		c.GitBranch = raw.GitBranch
	}
	if raw.GitRemote != "" {
		c.GitRemote = raw.GitRemote
	}
	if raw.ManifestPath != "" {
		c.ManifestPath = raw.ManifestPath
	}
	if len(raw.IgnoreDirs) > 0 {
		c.IgnoreDirs = raw.IgnoreDirs
	}
	if len(raw.FilePatterns) > 0 {
		c.FilePatterns = raw.FilePatterns
	}

	if raw.SyncInterval != "" {
		d, err := time.ParseDuration(raw.SyncInterval)
		if err != nil {
			return fmt.Errorf("sync_interval: %w", err)
		}
		c.SyncInterval = d
	}
	if raw.RescanInterval != "" {
		d, err := time.ParseDuration(raw.RescanInterval)
		if err != nil {
			return fmt.Errorf("rescan_interval: %w", err)
		}
		c.RescanInterval = d
	}
	return nil
}

// DefaultManifestPath returns the manifest location derived from a repo path.
func DefaultManifestPath(repoPath string) string {
	return filepath.Join(repoPath, ".env-sync-manifest.json")
}

// finalize resolves derived defaults (such as ManifestPath) and validates the
// resulting configuration.
func (c *Config) finalize() error {
	if c.ManifestPath == "" {
		c.ManifestPath = DefaultManifestPath(c.RepoPath)
	}
	return c.Validate()
}

// Validate ensures the configuration is internally consistent and usable.
func (c *Config) Validate() error {
	if c.ScanRoot == "" {
		return fmt.Errorf("scan_root must be set")
	}
	if c.RepoPath == "" {
		return fmt.Errorf("repo_path must be set")
	}
	if c.SyncInterval <= 0 {
		return fmt.Errorf("sync_interval must be positive")
	}
	if c.RescanInterval <= 0 {
		return fmt.Errorf("rescan_interval must be positive")
	}
	if c.GitBranch == "" {
		return fmt.Errorf("git_branch must be set")
	}
	if c.GitRemote == "" {
		return fmt.Errorf("git_remote must be set")
	}
	if len(c.FilePatterns) == 0 {
		return fmt.Errorf("file_patterns must not be empty")
	}
	return nil
}
