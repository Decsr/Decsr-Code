package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The parser follows Spec Kit's tasks template (templates/tasks-template.md)
// and its tasks and implement commands:
//
//   - a feature lives in specs/<NNN-feature>/ with spec.md, plan.md, tasks.md;
//   - tasks.md is split into "## Phase N: Name" headings (Setup, Foundational,
//     one per user story, Polish); "###" headings only group tasks inside a phase;
//   - a task is "- [ ] T001 [P] [US1] Description with path"; [X] or [x] marks
//     it done, [P] marks it parallelizable (different files, no unfinished
//     dependency), [USn] names the user story, the ID is T followed by digits;
//   - "**Checkpoint**" lines, notes and fenced examples are not tasks;
//   - implement runs phase by phase, sequential tasks in order, [P] tasks of a
//     phase together, and a task may say "(depends on T012, T013)".

// SpecTask is one task line of a Spec Kit tasks.md.
type SpecTask struct {
	ID       string // "T001", "" when the line has none
	Parallel bool   // carries [P]
	Story    string // "US1", "" when none
	Text     string // description without the ID and markers
	Line     string // the task line without checkbox, as written
	Done     bool
	Phase    int    // the phase number (from "Phase N", else the order)
	Name     string // the phase heading
	Needs    []string
}

// SpecList is a parsed tasks.md.
type SpecList struct {
	Tasks []SpecTask
}

var (
	specTaskRe  = regexp.MustCompile(`^\s*[-*+]\s+\[([ xX])\]\s*(.*)$`)
	specIDRe    = regexp.MustCompile(`^T(?:\d+|X+)$`)
	specStoryRe = regexp.MustCompile(`^\[(US\d+)\]$`)
	specPhaseRe = regexp.MustCompile(`(?i)^phase\s+(\d+)\b`)
	specDepRe   = regexp.MustCompile(`(?i)depends on ((?:T\d+[ ,and&]*)+)`)
	specRefRe   = regexp.MustCompile(`T\d+`)
)

// ParseSpecKit reads the text of a tasks.md. Tasks before any "##" heading
// belong to phase 0.
func ParseSpecKit(text string) SpecList {
	var list SpecList
	phase, name, fenced := 0, "", false
	order := 0
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			order++
			name = strings.TrimSpace(strings.TrimLeft(line, "# "))
			phase = order
			if m := specPhaseRe.FindStringSubmatch(name); m != nil {
				phase, _ = strconv.Atoi(m[1])
			}
			continue
		}
		m := specTaskRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		t := SpecTask{Done: m[1] != " ", Phase: phase, Name: name}
		fields := strings.Fields(m[2])
		t.Line = strings.Join(fields, " ")
		i := 0
		if i < len(fields) && specIDRe.MatchString(fields[i]) {
			t.ID = fields[i]
			i++
		}
		for ; i < len(fields); i++ {
			if fields[i] == "[P]" {
				t.Parallel = true
			} else if s := specStoryRe.FindStringSubmatch(fields[i]); s != nil {
				t.Story = s[1]
			} else {
				break
			}
		}
		t.Text = strings.Join(fields[i:], " ")
		if t.Text == "" {
			continue
		}
		if d := specDepRe.FindStringSubmatch(t.Text); d != nil {
			t.Needs = specRefRe.FindAllString(d[1], -1)
		}
		list.Tasks = append(list.Tasks, t)
	}
	return list
}

// SpecImport tells BuildSpecGraph what to import.
type SpecImport struct {
	Feature string // graph title
	Dir     string // feature dir relative to the project root, slash separated
	Tasks   string // tasks.md relative to the project root, slash separated; "" = Dir/tasks.md
	Phase   int    // 0 = every phase, else only that phase number
	HasQA   bool   // the qa role is defined; test tasks suggest it
}

// SpecResult is the graph with the counts for the chat line.
type SpecResult struct {
	Graph    *Graph
	Imported int
	Skipped  int // done tasks left out
	Phases   int // phases with an imported task
}

var specTestRe = regexp.MustCompile(`(?i)\btests?\b|_test\.go|\btest_\w+\.py|\.(?:spec|test)\.[jt]sx?\b`)

// SuggestSpecRole is "qa" for a task that writes tests when the role exists,
// else "coder".
func SuggestSpecRole(text string, hasQA bool) string {
	if hasQA && specTestRe.MatchString(text) {
		return "qa"
	}
	return "coder"
}

// BuildSpecGraph turns the open tasks of list into work nodes T1, T2, … (the
// session renumbers them on append).
//
// Dependencies: a task depends on every imported task of the previous imported
// phase; inside a phase a task without [P] depends on the previous task and on
// the run of [P] tasks right before it, a [P] task on the previous phase only;
// "(depends on T012)" in the text adds that task when it is imported.
func BuildSpecGraph(list SpecList, o SpecImport) SpecResult {
	res := SpecResult{Graph: &Graph{Task: "Spec Kit: " + o.Feature, Title: o.Feature}}
	var prevPhase, curPhase []string // node IDs
	phaseNo, inPhase := -1, false
	var run []string // [P] nodes directly before the current position
	last := ""
	byTaskID := map[string]string{}
	spec, plan := o.Dir+"/spec.md", o.Dir+"/plan.md"
	tasksFile := o.Tasks
	if tasksFile == "" {
		tasksFile = o.Dir + "/tasks.md"
	}
	for _, t := range list.Tasks {
		if o.Phase != 0 && t.Phase != o.Phase {
			continue
		}
		if t.Done {
			res.Skipped++
			continue
		}
		if !inPhase || t.Phase != phaseNo {
			if inPhase {
				prevPhase = curPhase
				res.Phases++
			}
			inPhase, phaseNo, curPhase, run, last = true, t.Phase, nil, nil, ""
		}
		id := fmt.Sprintf("T%d", res.Imported+1)
		res.Imported++
		n := &Node{
			ID:            id,
			Title:         specTitle(t),
			Description:   t.Line + "\nPhase: " + t.Name + "\nSpec: " + spec + ", plan: " + plan,
			Kind:          KindWork,
			Status:        StatusTodo,
			SuggestedRole: SuggestSpecRole(t.Text, o.HasQA),
			DependsOn:     append([]string(nil), prevPhase...),
		}
		if t.ID != "" {
			n.Spec = &SpecRef{File: tasksFile, TaskID: t.ID}
		}
		if !t.Parallel {
			n.DependsOn = appendUnique(n.DependsOn, run...)
			if last != "" {
				n.DependsOn = appendUnique(n.DependsOn, last)
			}
			run = nil
		} else {
			run = append(run, id)
		}
		for _, d := range t.Needs {
			if dep, ok := byTaskID[d]; ok {
				n.DependsOn = appendUnique(n.DependsOn, dep)
			}
		}
		if t.ID != "" {
			byTaskID[t.ID] = id
		}
		curPhase = append(curPhase, id)
		last = id
		res.Graph.Nodes = append(res.Graph.Nodes, n)
	}
	if inPhase {
		res.Phases++
	}
	return res
}

func appendUnique(dst []string, add ...string) []string {
	for _, a := range add {
		seen := false
		for _, d := range dst {
			seen = seen || d == a
		}
		if !seen {
			dst = append(dst, a)
		}
	}
	return dst
}

// specTitle is "T001 description", at most 80 characters.
func specTitle(t SpecTask) string {
	title := t.Text
	if t.ID != "" {
		title = t.ID + " " + title
	}
	if r := []rune(title); len(r) > 80 {
		title = strings.TrimSpace(string(r[:79])) + "…"
	}
	return title
}

// SpecFeatureName is the first heading of spec.md without a "Feature
// Specification:" prefix, or the feature directory name.
func SpecFeatureName(specText, dirName string) string {
	for _, raw := range strings.Split(specText, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if strings.HasPrefix(line, "# ") {
			h := strings.TrimSpace(line[2:])
			if i := strings.Index(h, ":"); i >= 0 && strings.Contains(strings.ToLower(h[:i]), "specification") {
				h = strings.TrimSpace(h[i+1:])
			}
			if h != "" {
				return h
			}
		}
	}
	return dirName
}

// FindSpecTasks resolves the argument of /import speckit to a tasks.md: a
// feature directory, a tasks.md, or (empty arg) the most recently modified
// specs/*/tasks.md under root.
func FindSpecTasks(root, arg string) (string, error) {
	if arg = strings.TrimSpace(arg); arg != "" {
		p := arg
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		st, err := os.Stat(p)
		if err != nil {
			return "", fmt.Errorf("%s not found", arg)
		}
		if st.IsDir() {
			p = filepath.Join(p, "tasks.md")
			if _, err := os.Stat(p); err != nil {
				return "", fmt.Errorf("no tasks.md in %s", arg)
			}
		}
		return p, nil
	}
	matches, _ := filepath.Glob(filepath.Join(root, "specs", "*", "tasks.md"))
	best, bestTime := "", int64(-1)
	for _, m := range matches {
		if st, err := os.Stat(m); err == nil && st.ModTime().UnixNano() > bestTime {
			best, bestTime = m, st.ModTime().UnixNano()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no specs/*/tasks.md found. Run Spec Kit's /speckit.tasks first or give a path")
	}
	return best, nil
}
