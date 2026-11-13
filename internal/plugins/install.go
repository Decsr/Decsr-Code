package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/config"
)

// gitTimeout bounds the git commands of one install.
var gitTimeout = 3 * time.Minute

// RepoURL is the clone URL of a GitHub owner/repo.
func RepoURL(repo string) string { return "https://github.com/" + repo + ".git" }

// Install installs a catalog plugin from GitHub. See InstallWith.
func Install(ctx context.Context, e CatalogEntry) error { return InstallWith(ctx, e, RepoURL) }

// InstallWith installs a plugin of the catalog: the repository is cloned
// (depth 1) into ~/.decsr/plugin-src/<owner>-<repo>, or updated when the clone
// exists, and the plugin directory is copied without .git to
// ~/.decsr/plugins/<name>. An existing copy is replaced only once the new one
// is complete. url maps owner/repo to the clone URL.
//
// Only the exact entries of the embedded catalog are installed: an entry that
// is not listed there, or differs from the listed one in repo or path, is
// refused before anything is touched.
func InstallWith(ctx context.Context, e CatalogEntry, url func(repo string) string) error {
	listed, ok := FindCatalog(e.Name)
	if !ok || listed.Repo != e.Repo || listed.Path != e.Path {
		return fmt.Errorf("plugin %q is not in the catalog", e.Name)
	}
	e = listed
	home, err := os.UserHomeDir()
	if err != nil {
		return errors.New("home directory not found")
	}
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	base := filepath.Join(home, config.GlobalDir)
	clone := filepath.Join(base, "plugin-src", strings.ReplaceAll(e.Repo, "/", "-"))
	if err := fetchRepo(ctx, clone, url(e.Repo)); err != nil {
		return err
	}
	from, ok := inside(clone, e.Path)
	if e.Path == "" {
		from, ok = clone, true
	}
	if !ok {
		return fmt.Errorf("%s: directory %q not found in %s", e.Name, e.Path, e.Repo)
	}
	data, found, _ := readFile(filepath.Join(from, manifestPath))
	if !found {
		return fmt.Errorf("%s: %s has no %s", e.Name, e.Repo, manifestPath)
	}
	var mf manifest
	if json.Unmarshal(data, &mf) != nil || mf.Name != e.Name {
		return fmt.Errorf("%s: the plugin in %s is named %q", e.Name, e.Repo, sanitize(mf.Name))
	}

	plugDir := filepath.Join(base, "plugins")
	if err := os.MkdirAll(plugDir, 0o750); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(base, ".install-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	staged := filepath.Join(tmp, "new")
	if err := copyTree(from, staged); err != nil {
		return fmt.Errorf("%s: copy: %w", e.Name, err)
	}
	dst := filepath.Join(plugDir, e.Name)
	old := filepath.Join(tmp, "old")
	_, statErr := os.Lstat(dst)
	replacing := statErr == nil
	if replacing {
		if err := os.Rename(dst, old); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, dst); err != nil {
		if replacing {
			_ = os.Rename(old, dst)
		}
		return err
	}
	return nil
}

// fetchRepo clones url into dir, or brings an existing clone up to date.
func fetchRepo(ctx context.Context, dir, url string) error {
	if isDir(filepath.Join(dir, ".git")) {
		if err := runGit(ctx, dir, "fetch", "--quiet", "--depth", "1", url, "HEAD"); err != nil {
			return err
		}
		if err := runGit(ctx, dir, "reset", "--quiet", "--hard", "FETCH_HEAD"); err != nil {
			return err
		}
		return runGit(ctx, dir, "clean", "--quiet", "-fdx")
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := runGit(ctx, "", "clone", "--quiet", "--depth", "1", url, dir); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	return nil
}

// runGit runs git in dir (the current directory when "") and reports the
// last line of its error output on failure.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "protocol.ext.allow=never"}, args...)...) // #nosec G204 -- fixed git subcommands; the URL comes from the embedded catalog
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("git %s: %w", args[0], ctx.Err())
	}
	msg := strings.TrimSpace(string(out))
	if i := strings.LastIndex(msg, "\n"); i >= 0 {
		msg = msg[i+1:]
	}
	return fmt.Errorf("git %s: %s", args[0], sanitize(msg))
}

// copyTree copies the regular files and directories of src to dst, leaving
// out .git and every link.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(target, 0o750)
		case !d.Type().IsRegular():
			return nil
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- regular file of a clone below the plugin-src directory
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm()|0o600) // #nosec G304 -- below a temp directory we created
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
