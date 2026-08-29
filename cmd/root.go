package cmd

import (
	"os"
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
	Use:           "mskill",
	Short:         "Fetch → Store → Link skill manager",
	Long:          "mskill is a Go-native replacement for npx skills — Resolve → Cache → Link with sparse git, fail-closed security, and human-gated trust.",
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
		if cacheDirOverride != "" {
			viper.Set("cache.dir", cacheDirOverride)
		}
		return nil
	},
}

// Execute is entrypoint from main.go.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// GetPaths returns resolved paths after PersistentPreRunE.
func GetPaths() config.Paths { return paths }

func init() {
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default $HOME/.mskill/config.yaml or $XDG_CONFIG_HOME/mskill/config.yaml)")
	rootCmd.PersistentFlags().StringVar(&cacheDirOverride, "cache-dir", "", "cache directory (default $XDG_CACHE_HOME/mskill or $HOME/.cache/mskill)")
	rootCmd.PersistentFlags().BoolVar(&verbose, "verbose", false, "verbose output")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")

	viper.SetEnvPrefix("MSKILL")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
}
