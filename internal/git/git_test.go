package git

import (
	"context"
	"testing"

	"github.com/spf13/viper"
)

func TestRunGitVersion(t *testing.T) {
	viper.Reset()
	viper.SetDefault("git.timeout", 0)
	viper.Set("git.bin", "git")
	out, err := Run(context.Background(), "", "version")
	if err != nil {
		t.Fatalf("git version failed: %v out %s", err, out)
	}
	if len(out) == 0 {
		t.Fatalf("empty git version output")
	}
}

func TestRunGitAliasAndCustomBin(t *testing.T) {
	viper.Reset()
	viper.Set("git.bin", "git")
	out, err := RunGit(context.Background(), "", "version")
	if err != nil {
		t.Fatalf("RunGit %v %s", err, out)
	}
	// invalid bin should error
	viper.Set("git.bin", "nonexistent-git-bin-xyz")
	_, err = Run(context.Background(), "", "version")
	if err == nil {
		t.Fatalf("expected error with bad bin")
	}
	viper.Reset()
}
