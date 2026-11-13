package plugins

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// upstream makes a work repo holding plugins/modern-cpp and a bare clone of
// it. It returns the work dir and the URL func that serves the bare clone.
func upstream(t *testing.T) (work string, urlFn func(string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	work = filepath.Join(base, "work")
	pl := filepath.Join(work, "plugins", "modern-cpp")
	write(t, filepath.Join(pl, ".claude-plugin", "plugin.json"), `{"name":"modern-cpp","description":"x"}`)
	write(t, filepath.Join(pl, "skills", "modern-cpp", "SKILL.md"), "---\ndescription: Modern C++\n---\nv1 body")
	write(t, filepath.Join(pl, "old.txt"), "old")
	write(t, filepath.Join(work, "README.md"), "outside the plugin")
	git(t, work, "init", "-q", "-b", "main")
	git(t, work, "add", ".")
	git(t, work, "commit", "-q", "-m", "v1")
	bare := filepath.Join(base, "bare.git")
	git(t, base, "clone", "-q", "--bare", work, bare)
	return work, func(repo string) string {
		if repo != "trailofbits/skills" {
			t.Errorf("unexpected repo %q", repo)
		}
		return "file://" + bare
	}
}

func TestInstallCopiesPluginWithoutGit(t *testing.T) {
	home, proj := setup(t)
	_, urlFn := upstream(t)
	e, _ := FindCatalog("modern-cpp")
	if err := InstallWith(context.Background(), e, urlFn); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(home, ".decsr", "plugins", "modern-cpp")
	for _, rel := range []string{".claude-plugin/plugin.json", "skills/modern-cpp/SKILL.md", "old.txt"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("%s missing: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "..", "..", "plugin-src", "trailofbits-skills", ".git")); err != nil {
		t.Errorf("clone not kept in plugin-src: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); err == nil {
		t.Error(".git was copied")
	}
	if _, err := os.Stat(filepath.Join(dst, "README.md")); err == nil {
		t.Error("file outside the plugin path was copied")
	}
	set := Load(proj)
	p := find(set, "modern-cpp")
	if p == nil || !p.Enabled || len(p.Skills) != 1 || p.Skills[0].ID != "modern-cpp:modern-cpp" {
		t.Fatalf("plugin after install = %+v", p)
	}
	// No temp leftovers next to the plugins.
	entries, _ := os.ReadDir(filepath.Join(home, ".decsr", "plugins"))
	if len(entries) != 1 {
		t.Errorf("plugins dir = %v", entries)
	}
}

func TestInstallUpdatesExistingCopy(t *testing.T) {
	home, _ := setup(t)
	work, urlFn := upstream(t)
	e, _ := FindCatalog("modern-cpp")
	if err := InstallWith(context.Background(), e, urlFn); err != nil {
		t.Fatal(err)
	}
	// Local edits and files of the old copy go away; upstream changes arrive.
	dst := filepath.Join(home, ".decsr", "plugins", "modern-cpp")
	write(t, filepath.Join(dst, "local.txt"), "local")
	if err := os.Remove(filepath.Join(work, "plugins", "modern-cpp", "old.txt")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(work, "plugins", "modern-cpp", "skills", "modern-cpp", "SKILL.md"), "---\ndescription: Modern C++\n---\nv2 body")
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "v2")
	bare := strings.TrimPrefix(urlFn("trailofbits/skills"), "file://")
	git(t, work, "push", "-q", bare, "HEAD:main")

	if err := InstallWith(context.Background(), e, urlFn); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dst, "skills", "modern-cpp", "SKILL.md"))
	if err != nil || !strings.Contains(string(body), "v2 body") {
		t.Fatalf("skill after update = %q err=%v", body, err)
	}
	for _, gone := range []string{"local.txt", "old.txt"} {
		if _, err := os.Stat(filepath.Join(dst, gone)); err == nil {
			t.Errorf("%s survived the update", gone)
		}
	}
}

func TestInstallRefusesUnknownSource(t *testing.T) {
	home, _ := setup(t)
	_, urlFn := upstream(t)
	e, _ := FindCatalog("modern-cpp")
	for name, bad := range map[string]CatalogEntry{
		"unknown name": {Name: "evil", Repo: e.Repo, Path: e.Path},
		"other repo":   {Name: e.Name, Repo: "evil/skills", Path: e.Path},
		"other path":   {Name: e.Name, Repo: e.Repo, Path: "plugins/other"},
		"escaping":     {Name: e.Name, Repo: e.Repo, Path: "../x"},
	} {
		if err := InstallWith(context.Background(), bad, urlFn); err == nil {
			t.Errorf("%s: install succeeded", name)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".decsr")); err == nil {
		t.Error("a refused install touched ~/.decsr")
	}
}

func TestInstallRejectsWrongManifestName(t *testing.T) {
	home, _ := setup(t)
	work, urlFn := upstream(t)
	write(t, filepath.Join(work, "plugins", "modern-cpp", ".claude-plugin", "plugin.json"), `{"name":"other"}`)
	git(t, work, "add", "-A")
	git(t, work, "commit", "-q", "-m", "rename")
	git(t, work, "push", "-q", strings.TrimPrefix(urlFn("trailofbits/skills"), "file://"), "HEAD:main")
	e, _ := FindCatalog("modern-cpp")
	err := InstallWith(context.Background(), e, urlFn)
	if err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, ".decsr", "plugins", "modern-cpp")); statErr == nil {
		t.Error("a bad plugin was installed")
	}
}

func TestInstallKeepsOldCopyOnFailure(t *testing.T) {
	home, _ := setup(t)
	_, urlFn := upstream(t)
	e, _ := FindCatalog("modern-cpp")
	if err := InstallWith(context.Background(), e, urlFn); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(home, ".decsr", "plugins", "modern-cpp")
	bad := func(string) string { return "file:///nonexistent/none.git" }
	if err := InstallWith(context.Background(), e, bad); err == nil {
		t.Fatal("expected a fetch error")
	}
	if _, err := os.Stat(filepath.Join(dst, "old.txt")); err != nil {
		t.Errorf("old copy lost: %v", err)
	}
}

func TestInstallCancelled(t *testing.T) {
	setup(t)
	_, urlFn := upstream(t)
	e, _ := FindCatalog("modern-cpp")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := InstallWith(ctx, e, urlFn); err == nil {
		t.Fatal("cancelled install succeeded")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("cancel was not prompt")
	}
}
