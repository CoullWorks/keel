package pack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// maxPackFile caps a single file copied out of a fetched pack. A pack is recipe
// YAML plus small hook scripts; nothing legitimate is anywhere near this, so the
// cap only stops a hostile pack from filling the disk with one giant file.
const maxPackFile = 64 << 20 // 64MB

// scpLike matches the scp-style git remote form user@host:path (e.g.
// git@github.com:owner/repo.git). The transport is implied SSH; there is no
// "ext::"/"file://" escape hatch in this form.
var scpLike = regexp.MustCompile(`^[\w.+-]+@[\w.-]+:.+$`)

// githubShorthand matches a bare GitHub owner/repo: two path segments of the
// characters GitHub allows in an owner or repo name (letters, digits, dot, dash,
// underscore), a single slash between them, and nothing else. No scheme, no
// deeper path, and — because a leading "." or "/" is not in the class — no local
// path like "./x" or "/abs/x". This is what lets `keel recipes add owner/repo`
// and `keel plugins add owner/repo` expand to the GitHub URL the help promises.
var githubShorthand = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ResolveSource normalises a pack source: owner/repo -> GitHub URL; full URLs and
// local directories are returned unchanged.
func ResolveSource(s string) string {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") {
		return s
	}
	if info, err := os.Stat(s); err == nil && info.IsDir() {
		return s
	}
	// A bare owner/repo (no scheme, no leading . or /, exactly two segments) is
	// the GitHub shorthand the help advertises. The previous guard tested for the
	// absence of os.PathSeparator, which on Linux is "/" — so every owner/repo
	// failed it and the expansion was dead code. Matching the shorthand shape
	// directly fixes that while still leaving local paths ("./x", "/abs", "a/b/c")
	// and full URLs to their own branches above / the passthrough below.
	if githubShorthand.MatchString(s) {
		return "https://github.com/" + s
	}
	return s
}

// validateGitSource allows only the transports we intend to clone over and
// rejects the dangerous ones git otherwise honours.
//
// git treats the clone URL as a transport selector, and some transports run
// arbitrary commands: "ext::sh -c <cmd>" executes a shell, "fd::" reads from a
// file descriptor, and "file://" / an absolute path let a pack source read
// anywhere on the machine — all before any trust prompt. We accept https, ssh,
// and the scp-like git@host:path form, and refuse everything else. The clone
// call additionally disables these transports with -c protocol.*.allow flags as
// defence in depth.
func validateGitSource(s string) error {
	switch {
	case strings.HasPrefix(s, "https://"), strings.HasPrefix(s, "ssh://"):
		return nil
	case scpLike.MatchString(s):
		// Reject anything smuggling a transport prefix into the scp-like form.
		if strings.Contains(s, "::") {
			return fmt.Errorf("refusing pack source %q: looks like a git transport helper", s)
		}
		return nil
	}
	return fmt.Errorf("refusing pack source %q: only https://, ssh:// and git@host:path remotes are allowed "+
		"(ext::, file://, fd:// and other git transports are blocked)", s)
}

// Fetch places the pack source into a fresh temp dir and returns its path plus
// the resolved short git commit ("" for a local dir). The caller cleans up.
func Fetch(ctx context.Context, source, ref string) (dir, commit string, err error) {
	resolved := ResolveSource(source)
	tmp, err := os.MkdirTemp("", "keel-pack-*")
	if err != nil {
		return "", "", err
	}
	if info, statErr := os.Stat(resolved); statErr == nil && info.IsDir() {
		if err := copyDir(resolved, tmp); err != nil {
			os.RemoveAll(tmp)
			return "", "", err
		}
		return tmp, "", nil
	}
	// A source or ref beginning with "-" would be read by git as an option
	// rather than a value (--upload-pack=… runs a command), so refuse it and
	// end the option list with "--" before the positional arguments.
	if strings.HasPrefix(resolved, "-") {
		os.RemoveAll(tmp)
		return "", "", fmt.Errorf("refusing pack source %q: it starts with a dash", resolved)
	}
	if strings.HasPrefix(ref, "-") {
		os.RemoveAll(tmp)
		return "", "", fmt.Errorf("refusing pack ref %q: it starts with a dash", ref)
	}
	// Reject dangerous git transports (ext::/file://…) before cloning.
	if err := validateGitSource(resolved); err != nil {
		os.RemoveAll(tmp)
		return "", "", err
	}
	// Belt and braces: disable the command-executing / local-file transports at
	// the config level too, so even a source that slipped the check above cannot
	// run a shell or read a local file during the clone.
	args := []string{
		"-c", "protocol.ext.allow=never",
		"-c", "protocol.file.allow=user",
		"-c", "protocol.fd.allow=user",
		"clone", "--depth", "1",
	}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", resolved, tmp)
	clone := exec.CommandContext(ctx, "git", args...)
	clone.Env = gitEnv()
	if out, e := clone.CombinedOutput(); e != nil {
		os.RemoveAll(tmp)
		return "", "", fmt.Errorf("git clone %s: %v: %s", resolved, e, strings.TrimSpace(string(out)))
	}
	rev := exec.CommandContext(ctx, "git", "-C", tmp, "rev-parse", "--short", "HEAD")
	rev.Env = gitEnv()
	c, e := rev.Output()
	if e != nil {
		os.RemoveAll(tmp)
		return "", "", fmt.Errorf("git rev-parse in %s: %w", tmp, e)
	}
	return tmp, strings.TrimSpace(string(c)), nil
}

// gitEnv returns the environment for a git child, with all interactive prompting
// disabled. Without this a clone of a private or bad remote blocks on the tty
// waiting for credentials — ignoring the context deadline — and a helper could
// surface stored credentials to a hostile remote. The empty GIT_ASKPASS/SSH_
// prompt settings make git fail fast instead.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		"GCM_INTERACTIVE=never",
	)
}

// Move relocates a fetched pack dir to its final install location.
func Move(from, to string) error {
	// to is the install location derived from the pack name; normalise it and
	// refuse a traversal so a crafted name cannot target a path outside the store.
	toAbs, err := filepath.Abs(to)
	if err != nil || strings.Contains(toAbs, "..") {
		return fmt.Errorf("refusing path outside %q", to)
	}
	to = toAbs
	if err := os.RemoveAll(to); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		// Only a cross-device link error justifies the copy fallback. Any other
		// rename failure (permissions, a bad path) is a real error to surface, not
		// to paper over with a copy that would likely fail the same way.
		if !errors.Is(err, syscall.EXDEV) {
			return err
		}
		if err2 := copyDir(from, to); err2 != nil { // cross-device fallback
			return err2
		}
		os.RemoveAll(from)
	}
	return nil
}

// copyDir recursively copies src into dst, skipping .git and symlinks.
//
// Symlinks are skipped rather than followed: a pack containing a link to
// ~/.ssh/id_rsa would otherwise have its target copied into the installed pack,
// turning "download a pack" into "read files elsewhere on the machine".
func copyDir(src, dst string) error {
	safeBase, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if rel == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		// Confine every copy-out target under dst: a hostile pack entry whose
		// relative path escapes (e.g. ../../etc) resolves outside safeBase and is
		// refused rather than written elsewhere on the machine.
		target, err := filepath.Abs(filepath.Join(dst, rel))
		if err != nil || !strings.HasPrefix(target, safeBase) {
			return fmt.Errorf("refusing path outside %q", dst)
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // devices, sockets and fifos are not pack content
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	// dst is a copy-out target under the install dir; normalise it and refuse a
	// traversal so a hostile relative path cannot land the file elsewhere.
	dstAbs, err := filepath.Abs(dst)
	if err != nil || strings.Contains(dstAbs, "..") {
		return fmt.Errorf("refusing path outside %q", dst)
	}
	dst = dstAbs
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	// Cap the copy so a hostile pack cannot fill the disk with one giant file.
	// Read one byte past the cap to tell "exactly at the cap" from "overran it".
	n, err := io.Copy(out, io.LimitReader(in, maxPackFile+1))
	if err != nil {
		return err
	}
	if n > maxPackFile {
		return fmt.Errorf("pack file %s exceeds the %d byte limit", src, maxPackFile)
	}
	// Carry the executable bit across. os.Create makes a 0666&^umask file, so
	// without this a plugin's bin/ scripts arrive unrunnable and its declared
	// commands fail with "permission denied" the first time anyone uses them.
	// Only the execute bits are taken; the rest stays at the default, so a
	// source with odd permissions cannot widen anything.
	if fi, err := in.Stat(); err == nil && fi.Mode()&0o111 != 0 {
		if cur, err := out.Stat(); err == nil {
			return out.Chmod(cur.Mode() | 0o111)
		}
	}
	return nil
}
