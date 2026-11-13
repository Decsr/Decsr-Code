package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/plugins"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

// wsWritePlugin writes a plugin named name under <base>/.decsr/plugins.
func wsWritePlugin(t *testing.T, base, name string, files map[string]string) {
	t.Helper()
	root := filepath.Join(base, ".decsr", "plugins", name)
	files[".claude-plugin/plugin.json"] = `{"name":"` + name + `","version":"1.2.3","description":"A test plugin"}`
	for rel, body := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// newPluginHarness builds a harness whose HOME holds a plugin "tb".
func newPluginHarness(t *testing.T) *wsHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsWritePlugin(t, home, "tb", map[string]string{
		"commands/diff-review.md": "---\ndescription: Review a diff\nargument-hint: \"<ref>\"\n---\nReview $ARGUMENTS now.",
		"skills/audit/SKILL.md":   "---\ndescription: Audit\n---\nAudit body",
		"agents/auditor.md":       "---\ndescription: Audits things\ntools: Read, Grep\n---\nYou audit.",
		"hooks/hooks.json":        "{}",
	})
	return newWSHarnessAt(t, t.TempDir())
}

func TestWorkspacePluginListing(t *testing.T) {
	h := newPluginHarness(t)
	h.typeCmd("/plugin")
	h.want("Plugins")
	h.want("tb 1.2.3 · user · enabled")
	h.want("/tb:diff-review")
	h.want("tb:audit")
	h.want("tb:auditor")
	h.want("hooks and MCP servers are not used by decsr")
}

func TestWorkspacePluginEmptyState(t *testing.T) {
	h := newWSHarness(t)
	h.typeCmd("/plugins")
	h.want("No plugins found")
	h.want("install-plugins.sh")
}

func TestWorkspacePluginCommandRuns(t *testing.T) {
	h := newPluginHarness(t)
	h.key("/")
	h.want("/tb:diff-review")
	h.want("<ref>")
	h.want("(plugin:tb)")
	h.key("esc")
	h.typeCmd("/tb:diff-review main")
	if !h.m.splitting {
		t.Fatalf("plugin command did not start a split:\n%s", h.view())
	}
	h.finishSplit()
	if len(h.tasks) != 1 || h.tasks[0] != "Review main now." {
		t.Fatalf("tasks = %q", h.tasks)
	}
}

func TestWorkspacePluginRolesNeverSaved(t *testing.T) {
	h := newPluginHarness(t)
	if _, ok := workspace.FindRole(h.m.cfg, "tb:auditor"); !ok {
		t.Fatal("plugin role missing from the model config")
	}
	if _, ok := h.m.effectiveAgents()["tb:auditor"]; ok {
		t.Error("plugin role would be saved into a profile")
	}
	if _, ok := h.m.cfg.Agents["tb:auditor"]; ok {
		t.Error("plugin role leaked into cfg.Agents")
	}
	// A config reload (role saved) keeps the plugin roles.
	h.m.cfg = h.m.withPluginAgents(h.m.cfg)
	if _, ok := workspace.FindRole(h.m.cfg, "tb:auditor"); !ok {
		t.Error("plugin role lost")
	}
}

func TestWorkspacePluginDisableEnable(t *testing.T) {
	h := newPluginHarness(t)
	h.toGraph()
	if err := h.eng.Assign("T1", "tb:auditor"); err != nil {
		t.Fatal(err)
	}
	h.m.g = h.eng.Snapshot()
	h.key("tab")

	h.typeCmd("/plugin disable tb")
	h.want("plugin tb disabled")
	if _, ok := workspace.FindRole(h.m.cfg, "tb:auditor"); ok {
		t.Error("role still offered after disable")
	}
	if got := h.m.g.Node("T1").Role; got != "" {
		t.Errorf("assignment of a disabled plugin role kept: %q", got)
	}
	for _, c := range h.m.commandList() {
		if strings.HasPrefix(c.name, "/tb:") {
			t.Errorf("command %s still offered", c.name)
		}
	}
	h.typeCmd("/plugin")
	h.want("tb 1.2.3 · user · disabled")
	h.key("esc")

	h.typeCmd("/plugin enable tb")
	h.want("plugin tb enabled")
	if _, ok := workspace.FindRole(h.m.cfg, "tb:auditor"); !ok {
		t.Error("role missing after enable")
	}

	h.typeCmd("/plugin enable nope")
	h.want("no plugin named")
	h.typeCmd("/plugin reload")
	h.want("plugins reloaded: 1 found")
}

func TestPluginLinesWrapInsteadOfTruncating(t *testing.T) {
	h := newWSHarness(t)
	p := plugins.Plugin{Name: "big", Scope: "user", Enabled: true}
	for i := 0; i < 12; i++ {
		p.Skills = append(p.Skills, plugins.Skill{ID: fmt.Sprintf("big:skill-number-%02d", i)})
	}
	h.m.plug = plugins.Set{Plugins: []plugins.Plugin{p}}
	text := strings.Join(h.m.pluginLines(), "\n")
	for i := 0; i < 12; i++ {
		if id := fmt.Sprintf("big:skill-number-%02d", i); !strings.Contains(text, id) {
			t.Errorf("%s missing from %q", id, text)
		}
	}
	for _, l := range h.m.pluginLines() {
		if w := ansi.StringWidth(l); w > 76 {
			t.Errorf("line of %d cells: %q", w, l)
		}
	}
}

func TestRolesPickerKeepsPluginRolesDistinct(t *testing.T) {
	names := []string{"feature-dev:code-architect", "feature-dev:code-explorer", "feature-dev:code-reviewer", "pr-review-toolkit:silent-failure-hunter", "pr-review-toolkit:type-design-analyzer"}
	var items []wsPickItem
	for _, n := range names {
		items = append(items, wsPickItem{kind: "role", name: n, desc: "Reviews code", meta: "claude:opus"})
	}
	for _, width := range []int{80, 120} {
		out := ansi.Strip(renderPicker("Roles", items, 0, "Esc", width, 20))
		for _, n := range names {
			_, agent, _ := strings.Cut(n, ":")
			if !strings.Contains(out, agent) {
				t.Errorf("width %d: agent %q of %q not visible:\n%s", width, agent, n, out)
			}
		}
		for _, l := range strings.Split(out, "\n") {
			if ansi.StringWidth(l) > width {
				t.Errorf("width %d: line too wide: %q", width, l)
			}
		}
	}
	if got := fitRoleName("pr-review-toolkit:silent-failure-hunter", maxPickNameW); ansi.StringWidth(got) != maxPickNameW || !strings.HasSuffix(got, ":silent-failure-hunter") {
		t.Errorf("fitRoleName = %q", got)
	}
}
