package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/wellknown"
)

var shareCmd = &cobra.Command{
	Use:   "share [dir]",
	Short: "Publish skills as a shareable website feed",
	Long: `Build a static skill feed (same format as 'mskill feed') and print the
shareable consumer commands. Serve the output dir as your site root —
no git clone required on the consumer side, with per-skill versioning.

  mskill share ./my-skills --base-url https://example.com --out ./public
  # serve ./public, then share:
  mskill get https://example.com/.well-known/agent-skills --skill <slug> --project -y`,
	Example: `  mskill share ./my-skills --base-url https://example.com --out ./public
  mskill share . --base-url https://example.com --version 2.0.0 --out ./public`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := "."
		if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
			src = strings.TrimSpace(args[0])
		}
		out, _ := cmd.Flags().GetString("out")
		baseURL, _ := cmd.Flags().GetString("base-url")
		version, _ := cmd.Flags().GetString("version")
		if strings.TrimSpace(out) == "" {
			return fmt.Errorf("missing --out: output dir required (e.g. --out ./public)")
		}
		if strings.TrimSpace(baseURL) == "" {
			return fmt.Errorf("missing --base-url: public origin required (e.g. --base-url https://example.com)")
		}
		if strings.TrimSpace(version) == "" {
			version = "1.0.0"
		}
		srcAbs, err := filepath.Abs(src)
		if err != nil {
			srcAbs = src
		}
		idx, dirs, err := wellknown.BuildFeed(srcAbs, strings.TrimSpace(baseURL), strings.TrimSpace(version))
		if err != nil {
			return err
		}
		indexPath, err := wellknown.WriteFeed(out, idx, dirs)
		if err != nil {
			return err
		}
		feedURL := strings.TrimSuffix(strings.TrimSpace(baseURL), "/") + wellknown.AgentSkillsPath
		cmd.Printf("shared %d skill(s) (index %s)\n", len(idx.Skills), indexPath)
		cmd.Printf("serve %s as your site root.\n", out)
		cmd.Printf("\ndiscover:\n  mskill get %s --list\n", feedURL)
		cmd.Printf("\ninstall:\n")
		for _, e := range idx.Skills {
			ver := e.Version
			ref := ""
			if ver != "" {
				ref = " --ref " + ver
			}
			cmd.Printf("  mskill get %s --skill %s%s --project -y\n", feedURL, e.Slug(), ref)
		}
		cmd.Printf("\nshare this URL: %s\n", feedURL)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(shareCmd)
	shareCmd.Flags().String("out", "", "output dir to write feed into (served as site root)")
	shareCmd.Flags().String("base-url", "", "public site origin (e.g. https://example.com)")
	shareCmd.Flags().String("version", "1.0.0", "default skill version when SKILL.md frontmatter has no version:")
	_ = shareCmd.MarkFlagRequired("out")
	_ = shareCmd.MarkFlagRequired("base-url")
}
