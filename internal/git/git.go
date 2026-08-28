package git

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/viper"
)

// Run executes git with args in dir, with timeout from viper (git.timeout, default 60s)
// and GIT_TERMINAL_PROMPT=0 and restricted protocols.
func Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	timeout := viper.GetDuration("git.timeout")
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	bin := viper.GetString("git.bin")
	if bin == "" {
		bin = "git"
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=https:http:ssh:git:file",
	)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// RunGit is an alias for Run (kept for spec naming).
func RunGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return Run(ctx, dir, args...)
}
