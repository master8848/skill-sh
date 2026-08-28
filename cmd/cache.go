package cmd

import (
	"github.com/spf13/cobra"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage cache",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cacheGCCmd = &cobra.Command{
	Use:   "gc",
	Short: "Garbage collect expired cache entries",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var cachePathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print cache and dot directories",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.Println("cache:", paths.CacheDir)
		cmd.Println("dot:", paths.DotDir)
		cmd.Println("config:", paths.ConfigFile)
		return nil
	},
}

var cacheCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove all cached repos",
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

func init() {
	rootCmd.AddCommand(cacheCmd)
	cacheCmd.AddCommand(cacheGCCmd)
	cacheCmd.AddCommand(cachePathCmd)
	cacheCmd.AddCommand(cacheCleanCmd)

	cacheGCCmd.Flags().Bool("dry-run", false, "show what would be removed without deleting")
}
