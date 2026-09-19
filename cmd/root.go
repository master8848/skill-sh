package cmd

import (
	"context"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"skill.sh/mskill/internal/config"
)

var (
	cfgFile          string
	cacheDirOverride string
	verbose          bool
	noColor          bool
	version          = "dev"
	paths            config.Paths
)

var rootCmd = &cobra.Command{
	Use:   "mskill",
	Short: "Install skills from GitHub into agent dirs",
	Example: `  mskill search anki --limit 5
  mskill get owner/repo --skill <name> --project`,
	SilenceUsage:  true,
	SilenceErrors: false,
	Version:       version,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cfgFile != "" {
			_ = os.Setenv("MSKILL_CONFIG", cfgFile)
		}
		if cacheDirOverride != "" {
			_ = os.Setenv("MSKILL_CACHE_DIR", cacheDirOverride)
		}
		p, err := config.ResolvePaths()
		if err != nil {
			return err
		}
		paths = p
		if err := config.EnsureDirs(paths); err != nil {
			return err
		}
		if err := config.InitViper(paths); err != nil {
			return err
		}
		if verbose {
			viper.Set("verbose", true)
		}
		if noColor {
			viper.Set("no-color", true)
		}
		if f := cmd.Root().PersistentFlags().Lookup("plain"); f != nil && f.Value.String() == "true" {
			viper.Set("no-color", true)
		}
		if cacheDirOverride != "" {
			viper.Set("cache.dir", cacheDirOverride)
		}
		// Propagate persistent --yes / -y (mskill --yes get ...) to viper.
		// BindPFlag already covers root persistent flag, but also check current cmd's
		// local flag and inherited persistent flag values explicitly for robustness.
		if viper.GetBool("yes") {
			viper.Set("yes", true)
		}
		if f := cmd.Root().PersistentFlags().Lookup("yes"); f != nil && f.Value.String() == "true" {
			viper.Set("yes", true)
		}
		if yesVal, err := cmd.Flags().GetBool("yes"); err == nil && yesVal {
			viper.Set("yes", true)
		}
		if pf := cmd.PersistentFlags().Lookup("yes"); pf != nil && pf.Value.String() == "true" {
			viper.Set("yes", true)
		}
		if inherited := cmd.InheritedFlags().Lookup("yes"); inherited != nil && inherited.Value.String() == "true" {
			viper.Set("yes", true)
		}
		return nil
	},
}

// Execute is entrypoint from main.go. Handles SIGINT gracefully via signal-aware context.
func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

// GetPaths returns resolved paths after PersistentPreRunE.
func GetPaths() config.Paths { return paths }

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.mskill/config.yaml or $XDG_CONFIG_HOME/mskill/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&cacheDirOverride, "cache-dir", "", "cache directory (default $XDG_CACHE_HOME/mskill or $HOME/.cache/mskill)")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "verbose output")
	// --plain is the agent-facing flag to strip color/decoration.
	// --no-color is kept as a hidden deprecated alias (same viper key).
	rootCmd.PersistentFlags().Bool("plain", false, "plain output, no color (for agents/pipes)")
	_ = viper.BindPFlag("no-color", rootCmd.PersistentFlags().Lookup("plain"))
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")
	_ = viper.BindPFlag("no-color", rootCmd.PersistentFlags().Lookup("no-color"))
	_ = rootCmd.PersistentFlags().MarkHidden("no-color")
	if rootCmd.PersistentFlags().Lookup("yes") == nil {
		rootCmd.PersistentFlags().BoolP("yes", "y", false, "skip confirmation")
	}
	if f := rootCmd.PersistentFlags().Lookup("yes"); f != nil {
		_ = viper.BindPFlag("yes", f)
	}
	// --offline / --cache: when set, reuse cached copy without network fetch (TTL-gated). Default is auto-fetch.
	if rootCmd.PersistentFlags().Lookup("offline") == nil {
		rootCmd.PersistentFlags().Bool("offline", false, "use cached copy without fetching (offline mode)")
		_ = viper.BindPFlag("offline", rootCmd.PersistentFlags().Lookup("offline"))
	}
	if rootCmd.PersistentFlags().Lookup("cache") == nil {
		rootCmd.PersistentFlags().Bool("cache", false, "alias for --offline: use cached copy")
		_ = viper.BindPFlag("cache", rootCmd.PersistentFlags().Lookup("cache"))
	}
	if rootCmd.PersistentFlags().Lookup("use-cache") == nil {
		rootCmd.PersistentFlags().Bool("use-cache", false, "alias for --offline")
		_ = viper.BindPFlag("cache.offline", rootCmd.PersistentFlags().Lookup("use-cache"))
	}
	// bind offline also to cache.offline for viper key consistency
	if f := rootCmd.PersistentFlags().Lookup("offline"); f != nil {
		_ = viper.BindPFlag("cache.offline", f)
	}

	viper.SetEnvPrefix("MSKILL")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Consistent version output: "mskill <version>" for both --version and `mskill version`
	rootCmd.SetVersionTemplate("mskill {{.Version}}\n")
	// Hide default cobra completion command (per UX decision: completion not surfaced in help)
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}
