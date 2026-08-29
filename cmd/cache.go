package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/cache"
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
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		deleted, err := cache.GC(paths, dryRun)
		if err != nil {
			return err
		}
		if dryRun {
			if len(deleted) == 0 {
				cmd.Println("dry-run: nothing to gc")
			} else {
				for _, p := range deleted {
					cmd.Printf("would remove %s\n", p)
				}
			}
		} else {
			for _, p := range deleted {
				fmt.Fprintf(os.Stderr, "removed %s\n", p)
			}
			cmd.Printf("gc removed %d entries\n", len(deleted))
		}
		return nil
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
		if err := cache.Clean(paths); err != nil {
			return err
		}
		cmd.Println("cache cleaned")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(cacheCmd)
	cacheCmd.AddCommand(cacheGCCmd)
	cacheCmd.AddCommand(cachePathCmd)
	cacheCmd.AddCommand(cacheCleanCmd)

	cacheGCCmd.Flags().Bool("dry-run", false, "show what would be removed without deleting")
	_ = fmt.Sprintf
}
