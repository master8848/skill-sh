package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Paths holds resolved filesystem locations.
type Paths struct {
	CacheDir   string
	DotDir     string
	ConfigFile string
	TrustFile  string
}

// ResolvePaths resolves cache/dot/config/trust honoring XDG and MSKILL_* overrides.
func ResolvePaths() (Paths, error) {
	home, _ := os.UserHomeDir()

	dotDir := os.Getenv("MSKILL_DOT_DIR")
	if dotDir == "" {
		if home == "" {
			dotDir = ".mskill"
		} else {
			dotDir = filepath.Join(home, ".mskill")
		}
	}
	dotDir = filepath.Clean(dotDir)

	configFile := os.Getenv("MSKILL_CONFIG")
	if configFile == "" {
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			configFile = filepath.Join(xdg, "mskill", "config.yaml")
		} else {
			configFile = filepath.Join(dotDir, "config.yaml")
		}
	}
	configFile = filepath.Clean(configFile)

	trustFile := filepath.Join(dotDir, "trust.json")
	trustFile = filepath.Clean(trustFile)

	cacheDir := os.Getenv("MSKILL_CACHE_DIR")
	if cacheDir == "" {
		if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
			cacheDir = filepath.Join(xdg, "mskill")
		} else if home != "" {
			cacheDir = filepath.Join(home, ".cache", "mskill")
		} else {
			cacheDir = filepath.Join(dotDir, "cache")
		}
	}
	cacheDir = filepath.Clean(cacheDir)

	return Paths{
		CacheDir:   cacheDir,
		DotDir:     dotDir,
		ConfigFile: configFile,
		TrustFile:  trustFile,
	}, nil
}

// EnsureDirs creates DotDir (0700) and CacheDir (0755) and parent of ConfigFile.
func EnsureDirs(p Paths) error {
	if err := os.MkdirAll(p.DotDir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(p.DotDir, 0o700)

	if err := os.MkdirAll(p.CacheDir, 0o755); err != nil {
		return err
	}
	if dir := filepath.Dir(p.ConfigFile); dir != "." && dir != p.DotDir {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// InitViper sets defaults, env prefix, and reads config file if present.
func InitViper(p Paths) error {
	viper.SetEnvPrefix("MSKILL")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	viper.SetDefault("cache.dir", p.CacheDir)
	viper.SetDefault("cache.ttl", 24*time.Hour)
	viper.SetDefault("cache.background_update", true)
	viper.SetDefault("cache.strategy", "fetch")
	viper.SetDefault("cache.shallow", true)
	viper.SetDefault("cache.filter", "blob:none")
	viper.SetDefault("cache.gc.enabled", true)
	viper.SetDefault("cache.gc.interval", 24*time.Hour)
	viper.SetDefault("cache.gc.max_age", 30*24*time.Hour)
	viper.SetDefault("cache.gc.max_size", "2GB")
	viper.SetDefault("sparse.enabled", true)
	viper.SetDefault("sparse.cone", true)
	viper.SetDefault("link.mode", "auto")
	viper.SetDefault("link.targets", []string{"claude", "agents", "project"})
	viper.SetDefault("git.bin", "git")
	viper.SetDefault("git.timeout", 60*time.Second)
	viper.SetDefault("security.require_password_for_risky", true)
	viper.SetDefault("security.trust_all", false)
	viper.SetDefault("security.password_hash", "")
	viper.SetDefault("security.salt", "")
	viper.SetDefault("security.password_set_at", "")
	viper.SetDefault("security.trust_enabled_at", "")
	viper.SetDefault("verbose", false)
	viper.SetDefault("no-color", false)

	viper.SetConfigFile(p.ConfigFile)
	viper.SetConfigType("yaml")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			return nil
		}
		if os.IsNotExist(err) {
			return nil
		}
		if strings.Contains(err.Error(), "Not Found") || strings.Contains(err.Error(), "no such file") {
			return nil
		}
		return err
	}
	return nil
}

// WriteConfig writes current viper config to p.ConfigFile with 0600 perms.
func WriteConfig(p Paths) error {
	if err := os.MkdirAll(filepath.Dir(p.ConfigFile), 0o700); err != nil {
		return err
	}
	if err := viper.WriteConfigAs(p.ConfigFile); err != nil {
		if err2 := viper.SafeWriteConfigAs(p.ConfigFile); err2 != nil {
			return err
		}
	}
	return os.Chmod(p.ConfigFile, 0o600)
}

// RandomSalt returns hex-encoded random bytes of length n.
func RandomSalt(n int) (string, error) {
	if n <= 0 {
		n = 16
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
