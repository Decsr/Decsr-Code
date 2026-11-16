package automation

import (
	"errors"
	"strings"
	"testing"
)

type fakeCrontab struct {
	content string
	writes  int
	listErr error
}

func (f *fakeCrontab) List() (string, error) { return f.content, f.listErr }
func (f *fakeCrontab) Write(c string) error  { f.content = c; f.writes++; return nil }

func testAuto(name string) Automation {
	c, _ := ParseCron("0 3 * * 1-5")
	return Automation{Name: name, Schedule: "0 3 * * 1-5", cron: c}
}

func TestLineFormat(t *testing.T) {
	l, err := Line(testAuto("deps-bump"), "/home/u/proj", "/usr/local/bin/decsr", "/usr/bin:/bin")
	if err != nil {
		t.Fatal(err)
	}
	want := `0 3 * * 1-5 cd '/home/u/proj' && PATH='/usr/bin:/bin' '/usr/local/bin/decsr' automation run deps-bump --tmux >> '/home/u/proj/.decsr/automations/deps-bump.log' 2>&1 # decsr:/home/u/proj:deps-bump`
	if l != want {
		t.Errorf("line\n got %s\nwant %s", l, want)
	}
}

func TestLineQuoting(t *testing.T) {
	l, err := Line(testAuto("a"), "/home/it's me/my proj", "/opt/de csr/decsr", "/bin:/x y")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`cd '/home/it'\''s me/my proj'`,
		`PATH='/bin:/x y'`,
		`'/opt/de csr/decsr'`,
		`>> '/home/it'\''s me/my proj/.decsr/automations/a.log'`,
	} {
		if !strings.Contains(l, want) {
			t.Errorf("line misses %s:\n%s", want, l)
		}
	}
	pct, _ := Line(testAuto("a"), "/home/100%/p", "/bin/decsr", "/bin")
	if strings.Contains(strings.ReplaceAll(pct, `\%`, ""), "%") {
		t.Errorf("unescaped %% in %s", pct)
	}
	f := &fakeCrontab{}
	installOne(t, f, "/home/100%/p", "a", "/bin/decsr")
	installOne(t, f, "/home/100%/p", "a", "/bin/decsr")
	if strings.Count(f.content, "\n") != 1 || f.writes != 1 {
		t.Errorf("percent path not idempotent:\n%s", f.content)
	}
	if rm, _ := Uninstall(f, "/home/100%/p", nil); len(rm) != 1 {
		t.Errorf("percent path not removed: %v", rm)
	}
	if _, err := Line(testAuto("a"), "/home/a\nb", "/bin/decsr", "/bin"); err == nil {
		t.Error("line break accepted")
	}
	if _, err := Line(testAuto("a"), "rel/path", "/bin/decsr", "/bin"); err == nil {
		t.Error("relative path accepted")
	}
}

func TestLineBinary(t *testing.T) {
	l, _ := Line(testAuto("a"), "/p", "/opt/it's/decsr", "/bin")
	if bin, ok := LineBinary(l); !ok || bin != "/opt/it's/decsr" {
		t.Errorf("LineBinary = %q, %v", bin, ok)
	}
	if _, ok := LineBinary("0 1 * * * echo hi"); ok {
		t.Error("foreign line parsed")
	}
}

func installOne(t *testing.T, c Crontab, project, name, exe string) []string {
	t.Helper()
	l, err := Line(testAuto(name), project, exe, "/bin")
	if err != nil {
		t.Fatal(err)
	}
	w, err := Install(c, project, map[string]string{name: l}, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestInstallEmptyCrontab(t *testing.T) {
	f := &fakeCrontab{}
	w := installOne(t, f, "/p", "a", "/bin/decsr")
	if len(w) != 1 || f.content != w[0]+"\n" || f.writes != 1 {
		t.Errorf("content %q writes %d", f.content, f.writes)
	}
}

func TestInstallKeepsOtherLinesAndIsIdempotent(t *testing.T) {
	other := "MAILTO=me\n0 1 * * * /usr/bin/backup\n# decsr:/other:a\n"
	f := &fakeCrontab{content: other}
	installOne(t, f, "/p", "a", "/bin/decsr")
	installOne(t, f, "/p", "b", "/bin/decsr")
	after2 := f.content
	writes := f.writes
	installOne(t, f, "/p", "a", "/bin/decsr")
	installOne(t, f, "/p", "b", "/bin/decsr")
	if f.content != after2 || f.writes != writes {
		t.Errorf("second install changed the crontab or wrote again (%d -> %d)", writes, f.writes)
	}
	if !strings.HasPrefix(f.content, other) {
		t.Errorf("unrelated lines changed:\n%s", f.content)
	}
	if strings.Count(f.content, "# decsr:/p:a") != 1 || strings.Count(f.content, "# decsr:/p:b") != 1 {
		t.Errorf("duplicate lines:\n%s", f.content)
	}
}

func TestInstallReplacesMarkedLineInPlace(t *testing.T) {
	f := &fakeCrontab{}
	installOne(t, f, "/p", "a", "/old/decsr")
	f.content += "5 5 * * * after\n"
	installOne(t, f, "/p", "a", "/new/decsr")
	lines := strings.Split(strings.TrimSpace(f.content), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "/new/decsr") || lines[1] != "5 5 * * * after" {
		t.Errorf("not replaced in place:\n%s", f.content)
	}
}

func TestInstallCollapsesDuplicateMarkedLines(t *testing.T) {
	f := &fakeCrontab{}
	installOne(t, f, "/p", "a", "/old/decsr")
	f.content += f.content
	installOne(t, f, "/p", "a", "/new/decsr")
	if strings.Count(f.content, "# decsr:/p:a") != 1 {
		t.Errorf("duplicates left:\n%s", f.content)
	}
}

func TestInstallListError(t *testing.T) {
	f := &fakeCrontab{listErr: errors.New("boom")}
	if _, err := Install(f, "/p", nil, nil); err == nil {
		t.Error("list error swallowed")
	}
}

func TestUninstall(t *testing.T) {
	f := &fakeCrontab{content: "0 1 * * * keep\n"}
	installOne(t, f, "/p", "a", "/bin/decsr")
	installOne(t, f, "/p", "b", "/bin/decsr")
	installOne(t, f, "/q", "a", "/bin/decsr")
	removed, err := Uninstall(f, "/p", []string{"a"})
	if err != nil || len(removed) != 1 || !strings.HasSuffix(removed[0], "# decsr:/p:a") {
		t.Fatalf("removed %v, %v", removed, err)
	}
	if strings.Contains(f.content, "# decsr:/p:a") || !strings.Contains(f.content, "# decsr:/p:b") || !strings.Contains(f.content, "# decsr:/q:a") || !strings.Contains(f.content, "keep") {
		t.Errorf("wrong lines removed:\n%s", f.content)
	}
	removed, _ = Uninstall(f, "/p", nil)
	if len(removed) != 1 || strings.Contains(f.content, "# decsr:/p:") || !strings.Contains(f.content, "# decsr:/q:a") {
		t.Errorf("uninstall all: %v\n%s", removed, f.content)
	}
	writes := f.writes
	if removed, _ := Uninstall(f, "/p", nil); len(removed) != 0 || f.writes != writes {
		t.Error("uninstall without lines wrote the crontab")
	}
}

func TestInstalled(t *testing.T) {
	f := &fakeCrontab{}
	installOne(t, f, "/p", "a", "/bin/decsr")
	got, err := Installed(f, "/p")
	if err != nil || !got["a"] || got["b"] {
		t.Errorf("Installed = %v, %v", got, err)
	}
	lines, _ := InstalledLines(f, "/p")
	if !strings.Contains(lines["a"], "automation run a --tmux") {
		t.Errorf("InstalledLines = %v", lines)
	}
}
