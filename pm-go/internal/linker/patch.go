package linker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/nubjs/nub/pm-go/internal/fsutil"
)

type PatchError struct{ Key, Message string }

func (e *PatchError) Code() string { return "ERR_AUBE_PATCH_FAILED" }

func (e *PatchError) Error() string {
	return fmt.Sprintf("failed to apply patch for %s: %s", e.Key, e.Message)
}

// ApplyPatch applies git-style or plain unified diffs to one materialized
// package. Writes replace inodes, so modifying hardlinked content cannot alter
// the CAS or other installs. Sections apply in order, as in the Rust engine.
func ApplyPatch(ctx context.Context, packageDir, text string) error {
	if !filepath.IsAbs(packageDir) {
		return fmt.Errorf("patch application requires an absolute package directory")
	}
	sections := splitPatchSections(text)
	if len(sections) == 0 {
		return fmt.Errorf("patch contained no parseable file sections")
	}
	for _, section := range sections {
		if err := ctx.Err(); err != nil {
			return err
		}
		if section.path == nil {
			return fmt.Errorf("patch section missing file path")
		}
		relative := *section.path
		if !safePatchPath(relative) {
			return fmt.Errorf("patch file path escapes package: %q", relative)
		}
		if err := patchPathHasNoLinks(packageDir, relative); err != nil {
			return fmt.Errorf("patch target contains symlink: %w", err)
		}
		target := filepath.Join(packageDir, filepath.FromSlash(relative))
		_, statErr := os.Stat(target)
		exists := statErr == nil
		original := ""
		if exists {
			data, err := os.ReadFile(target)
			if err != nil {
				return fmt.Errorf("failed to read %s: %w", target, err)
			}
			if !utf8.Valid(data) {
				return fmt.Errorf("failed to read %s: stream did not contain valid UTF-8", target)
			}
			original = string(data)
		}
		if section.deletion {
			if exists {
				if err := unlinkBinFile(target); err != nil {
					return fmt.Errorf("failed to remove %s: %w", target, err)
				}
			}
			continue
		}
		crlf := strings.Contains(original, "\r\n")
		if crlf {
			original = strings.ReplaceAll(original, "\r\n", "\n")
		}
		hunks, err := parsePatchHunks(section.body)
		if err != nil {
			return fmt.Errorf("failed to parse patch for %s: %w", relative, err)
		}
		patched, err := applyPatchHunks(original, hunks)
		if err != nil {
			return fmt.Errorf("failed to apply patch for %s: %w", relative, err)
		}
		if crlf {
			patched = strings.ReplaceAll(strings.ReplaceAll(patched, "\n", "\r\n"), "\r\r\n", "\r\n")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if runtime.GOOS == "windows" && exists {
			if err := unlinkBinFile(target); err != nil {
				return fmt.Errorf("failed to unlink %s: %w", target, err)
			}
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("failed to write patched file into place %s: %w", target, err)
		}
		if err := fsutil.WriteDefault(target, []byte(patched)); err != nil {
			return fmt.Errorf("failed to write patched file into place %s: %w", target, err)
		}
	}
	return nil
}

func safePatchPath(relative string) bool {
	if relative == "" || strings.ContainsAny(relative, "\x00\\") || strings.HasPrefix(relative, "/") || len(relative) >= 2 && relative[1] == ':' {
		return false
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func patchPathHasNoLinks(root, relative string) error {
	cursor := root
	for _, part := range strings.Split(relative, "/") {
		if part == "" || part == "." {
			continue
		}
		cursor = filepath.Join(cursor, part)
		info, err := os.Lstat(cursor)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return fmt.Errorf("stat %s: %w", cursor, err)
		}
		if patchPathIsLink(info) {
			return fmt.Errorf("%s", cursor)
		}
	}
	return nil
}
