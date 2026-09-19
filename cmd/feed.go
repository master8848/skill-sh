package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"skill.sh/mskill/internal/wellknown"
)

var feedCmd = &cobra.Command{
	Use:   "feed [dir]",
	Short: "Build a website skill feed (no git clone needed)",
	Long: `Scan a directory for skills and write a static feed a website can serve.

  Output layout (serve <out> as your site root):

    <out>/.well-known/agent-skills/index.json   (primary, RFC 0.2)
    <out>/.well-known/skills/index.json         (legacy v0.1 alias)
    <out>/skills/<slug>/SKILL.md (+ sibling files)

  Consumers install without git clone:

    mskill get https://example.com/.well-known/agent-skills --list
    mskill get https://example.com/.well-known/agent-skills --skill <slug> --project -y

Versioning: --version sets the default entry version (frontmatter "version:"
wins per skill). Install a pinned version with --ref:

    mskill get https://example.com/.well-known/agent-skills --skill <slug> --ref 1.2.0`,
	Example: `  mskill feed ./my-skills --base-url https://example.com --out ./public
  mskill feed . --base-url https://example.com --version 1.2.0 --out ./public`,
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
			return fmt.Errorf("missing --out: feed output dir required (e.g. --out ./public)")
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
		cmd.Printf("feed: %d skill(s) -> %s\n", len(idx.Skills), indexPath)
		for _, e := range idx.Skills {
			ver := e.Version
			if ver == "" {
				ver = version
			}
			cmd.Printf("  - %s@%s  %s\n", e.Slug(), ver, e.URL)
		}
		cmd.Printf("serve %s as your site root, then:\n", out)
		cmd.Printf("  mskill get %s --list\n", feedURL)
		if len(idx.Skills) > 0 {
			cmd.Printf("  mskill get %s --skill %s --project -y\n", feedURL, idx.Skills[0].Slug())
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(feedCmd)
	feedCmd.Flags().String("out", "", "output dir to write feed into (served as site root)")
	feedCmd.Flags().String("base-url", "", "public site origin (e.g. https://example.com)")
	feedCmd.Flags().String("version", "1.0.0", "default skill version when SKILL.md frontmatter has no version:")
	_ = feedCmd.MarkFlagRequired("out")
	_ = feedCmd.MarkFlagRequired("base-url")
}
