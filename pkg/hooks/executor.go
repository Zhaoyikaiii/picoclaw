// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package hooks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	// DefaultTimeout is the maximum time a hook script can run.
	DefaultTimeout = 30 * time.Second

	// MaxOutputBytes is the maximum bytes captured from hook stdout.
	MaxOutputBytes = 64 * 1024 // 64KB
)

// dangerousCmdPatterns blocks destructive/dangerous commands from running
// as hook scripts. These patterns are checked against the lowercased command
// string before execution. The list mirrors the safety patterns used by the
// exec tool (pkg/tools/shell.go) but is defined here to avoid circular imports.
//
// Hooks are user-configured (not LLM-generated), so this is defense-in-depth:
// protecting against typos, copy-paste accidents, and config file corruption.
var dangerousCmdPatterns = []*regexp.Regexp{
	// Destructive file operations
	regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
	regexp.MustCompile(`\bdel\s+/[fq]\b`),
	regexp.MustCompile(`\brmdir\s+/s\b`),

	// Disk wiping / formatting
	regexp.MustCompile(`\b(format|mkfs|diskpart)\b[.\s]`),
	regexp.MustCompile(`\bdd\s+if=`),
	regexp.MustCompile(
		`>\s*/dev/(sd[a-z]|hd[a-z]|vd[a-z]|xvd[a-z]|nvme\d|mmcblk\d|loop\d|dm-\d|md\d|sr\d|nbd\d)`,
	),

	// System control
	regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),

	// Fork bombs
	regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),

	// Privilege escalation
	regexp.MustCompile(`\bsudo\b`),
	regexp.MustCompile(`\bchmod\s+[0-7]{3,4}\b`),
	regexp.MustCompile(`\bchown\b`),

	// Remote code execution via pipe
	regexp.MustCompile(`\bcurl\b.*\|\s*(sh|bash)`),
	regexp.MustCompile(`\bwget\b.*\|\s*(sh|bash)`),

	// Piped shell execution
	regexp.MustCompile(`\|\s*sh\b`),
	regexp.MustCompile(`\|\s*bash\b`),
}

// Executor runs hook commands via os/exec.
type Executor struct {
	Timeout        time.Duration
	MaxOutputBytes int
}

// NewExecutor creates an Executor with default settings.
func NewExecutor() *Executor {
	return &Executor{
		Timeout:        DefaultTimeout,
		MaxOutputBytes: MaxOutputBytes,
	}
}

// Run executes a command with the given stdin data and extra environment variables.
// It returns the stdout output (truncated to MaxOutputBytes) and any error.
// The command inherits the current process environment plus the extra env vars.
func (e *Executor) Run(ctx context.Context, command string, stdinData []byte, extraEnv []string) HookResult {
	if strings.TrimSpace(command) == "" {
		return HookResult{Err: fmt.Errorf("empty hook command")}
	}

	// Safety guard: block obviously dangerous commands.
	// Hook commands are user-configured, so this is defense-in-depth.
	if reason := guardCommand(command); reason != "" {
		return HookResult{Err: fmt.Errorf("hook command blocked: %s", reason)}
	}

	// Note: ~ expansion is intentionally left to the shell.
	// On Unix, sh -c handles ~ natively in all positions.
	// On Windows, PowerShell also expands ~ (resolves to $HOME).
	// Go-side expansion would only cover the leading ~ case, miss mid-command
	// occurrences (e.g. "python3 ~/.picoclaw/hook.py"), and require separate
	// handling of path separators (/ vs \) per platform.

	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cmdCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(cmdCtx, "sh", "-c", command)
	}

	// Inherit current env + hook-specific vars
	cmd.Env = append(os.Environ(), extraEnv...)

	// Pass payload via stdin
	if len(stdinData) > 0 {
		cmd.Stdin = bytes.NewReader(stdinData)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	maxOutput := e.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = MaxOutputBytes
	}
	if len(output) > maxOutput {
		output = output[:maxOutput]
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			return HookResult{
				Output: output,
				Err:    fmt.Errorf("hook timed out after %v: %s", timeout, command),
			}
		}
		stderrStr := stderr.String()
		if len(stderrStr) > 1024 {
			stderrStr = stderrStr[:1024]
		}
		return HookResult{
			Output: output,
			Err:    fmt.Errorf("hook failed: %w (stderr: %s)", err, stderrStr),
		}
	}

	return HookResult{Output: output}
}

// guardCommand checks a command against dangerous patterns.
// Returns a non-empty reason string if the command should be blocked.
func guardCommand(command string) string {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, pattern := range dangerousCmdPatterns {
		if pattern.MatchString(lower) {
			return fmt.Sprintf("dangerous pattern detected: %s", pattern.String())
		}
	}
	return ""
}
