package automation

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/executil"
	"github.com/Decsr/Decsr-Code/internal/multiplexer"
)

// Crontab reads and replaces the crontab of the current user.
type Crontab interface {
	List() (string, error)
	Write(content string) error
}

// ExecCrontab runs the real crontab program.
type ExecCrontab struct{}

// List returns the crontab; a user without one has an empty crontab.
func (ExecCrontab) List() (string, error) {
	out, err := executil.Command("crontab", "-l").CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(out)), "no crontab for") {
			return "", nil
		}
		return "", fmt.Errorf("crontab -l: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Write replaces the crontab.
func (ExecCrontab) Write(content string) error {
	cmd := executil.Command("crontab", "-")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab -: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Marker is the trailing comment that tags the crontab line of an automation.
func Marker(project, name string) string { return "# decsr:" + project + ":" + name }

// quote single-quotes s for sh and escapes the "%" that cron turns into a
// line break.
func quote(s string) string { return cronEsc(multiplexer.ShellQuote(s)) }

// cronEsc escapes the "%" that cron turns into a line break.
func cronEsc(s string) string { return strings.ReplaceAll(s, "%", `\%`) }

// markerIn is the marker as it stands in a crontab line.
func markerIn(project, name string) string { return cronEsc(Marker(project, name)) }

// Line is the crontab line that starts an automation in a detached tmux
// session. project and exe must be absolute.
func Line(a Automation, project, exe, path string) (string, error) {
	for _, s := range []string{project, exe, path} {
		if strings.ContainsAny(s, "\n\r") {
			return "", errors.New("a path with a line break cannot go in a crontab")
		}
	}
	if !filepath.IsAbs(project) || !filepath.IsAbs(exe) {
		return "", errors.New("the project and the decsr binary need absolute paths")
	}
	return fmt.Sprintf("%s cd %s && PATH=%s %s automation run %s --tmux >> %s 2>&1 %s",
		a.Schedule, quote(project), quote(path), quote(exe), a.Name,
		quote(LogPath(project, a.Name)), markerIn(project, a.Name)), nil
}

func splitLines(content string) []string {
	content = strings.TrimRight(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// Install adds or replaces the crontab lines of the automations (one per
// name, keyed by the marker). It returns the lines it wrote. The crontab is
// only rewritten when it changes.
func Install(c Crontab, project string, lines map[string]string, order []string) ([]string, error) {
	cur, err := c.List()
	if err != nil {
		return nil, err
	}
	existing := splitLines(cur)
	done := map[string]bool{}
	var out []string
	for _, l := range existing {
		replaced := false
		for _, name := range order {
			if strings.HasSuffix(strings.TrimSpace(l), markerIn(project, name)) {
				replaced = true
				if !done[name] {
					out = append(out, lines[name])
					done[name] = true
				}
				break
			}
		}
		if !replaced {
			out = append(out, l)
		}
	}
	var written []string
	for _, name := range order {
		if !done[name] {
			out = append(out, lines[name])
		}
		written = append(written, lines[name])
	}
	if joinLines(out) != joinLines(existing) {
		if err := c.Write(joinLines(out)); err != nil {
			return nil, err
		}
	}
	return written, nil
}

// Uninstall removes the marked lines of the named automations of project, or
// of all of them when names is empty. It returns the removed lines.
func Uninstall(c Crontab, project string, names []string) ([]string, error) {
	cur, err := c.List()
	if err != nil {
		return nil, err
	}
	var re *regexp.Regexp
	if len(names) == 0 {
		re = regexp.MustCompile(`\s` + regexp.QuoteMeta(markerIn(project, "")) + `[a-z0-9-]+$`)
	} else {
		alts := make([]string, len(names))
		for i, n := range names {
			alts[i] = regexp.QuoteMeta(n)
		}
		re = regexp.MustCompile(`\s` + regexp.QuoteMeta(markerIn(project, "")) + `(` + strings.Join(alts, "|") + `)$`)
	}
	var keep, removed []string
	for _, l := range splitLines(cur) {
		if re.MatchString(strings.TrimSpace(l)) {
			removed = append(removed, l)
			continue
		}
		keep = append(keep, l)
	}
	if len(removed) > 0 {
		if err := c.Write(joinLines(keep)); err != nil {
			return nil, err
		}
	}
	return removed, nil
}

// Installed reports which automations of project have a marked line.
func Installed(c Crontab, project string) (map[string]bool, error) {
	cur, err := c.List()
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`\s` + regexp.QuoteMeta(markerIn(project, "")) + `([a-z0-9-]+)$`)
	out := map[string]bool{}
	for _, l := range splitLines(cur) {
		if m := re.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			out[m[1]] = true
		}
	}
	return out, nil
}

// LineBinary extracts the decsr binary path of a crontab line written by
// Line, for the doctor check. ok is false when the line has another shape.
func LineBinary(line string) (string, bool) {
	_, rest, ok := strings.Cut(line, " && PATH=")
	if !ok {
		return "", false
	}
	_, rest, ok = strings.Cut(rest, "' '")
	if !ok {
		return "", false
	}
	bin, _, ok := strings.Cut(rest, "' automation run ")
	if !ok {
		return "", false
	}
	return strings.ReplaceAll(strings.ReplaceAll(bin, `'\''`, "'"), `\%`, "%"), true
}

// InstalledLines returns the marked lines of project, keyed by name.
func InstalledLines(c Crontab, project string) (map[string]string, error) {
	cur, err := c.List()
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`\s` + regexp.QuoteMeta(markerIn(project, "")) + `([a-z0-9-]+)$`)
	out := map[string]string{}
	for _, l := range splitLines(cur) {
		if m := re.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			out[m[1]] = l
		}
	}
	return out, nil
}
