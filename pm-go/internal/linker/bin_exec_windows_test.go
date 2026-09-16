package linker

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

func cmdShimCommand(t *testing.T, shim string, args ...string) *exec.Cmd {
	t.Helper()
	parts := append([]string{shim}, args...)
	for i, part := range parts {
		if strings.ContainsAny(part, "\"\r\n") {
			t.Fatal("unsupported cmd fixture argument", part)
		}
		parts[i] = `"` + part + `"`
	}
	command := exec.CommandContext(t.Context(), "cmd.exe")
	// cmd.exe uses its own quoting grammar, not CommandLineToArgvW. Bypass
	// os/exec's default argument encoder for this complete /s /c command.
	command.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + strings.Join(parts, " ") + `"`}
	return command
}
