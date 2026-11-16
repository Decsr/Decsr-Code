package runner

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// ActivityKind classifies what an agent is doing.
type ActivityKind string

const (
	ActThinking ActivityKind = "thinking" // reasoning / thought text
	ActMessage  ActivityKind = "message"  // assistant prose (summarised)
	ActTool     ActivityKind = "tool"     // generic tool call (name in Title)
	ActCommand  ActivityKind = "command"  // shell command; Title = command line
	ActRead     ActivityKind = "read"     // file read; Title = path
	ActEdit     ActivityKind = "edit"     // file edited; Title = path; Added/Removed
	ActWrite    ActivityKind = "write"    // file created/overwritten; Title = path; Added/Removed
	ActSearch   ActivityKind = "search"   // grep/glob/web search; Title = query
	ActError    ActivityKind = "error"
	ActWaiting  ActivityKind = "waiting" // rate limited / retrying / waiting on something
)

// ActivityState is the lifecycle state of an Activity.
type ActivityState string

const (
	ActRunning ActivityState = "running"
	ActDone    ActivityState = "done"
	ActFailed  ActivityState = "failed"
)

// Activity is one structured item of agent work. A later Activity with the
// same ID replaces the earlier one (for example running -> done). Activities
// never replace Token.Text: the final text of a run is parsed from Text only.
type Activity struct {
	ID       string // stable per item so a later update (running → done) replaces the earlier one
	Kind     ActivityKind
	State    ActivityState
	Title    string // one line, ≤ 200 chars
	Detail   string // bounded excerpt (input/output), ≤ 2000 chars, may be ""
	Added    int
	Removed  int
	ExitCode *int
	At       time.Time
	Duration time.Duration // set when State != running, if known
}

const (
	maxActivityTitle  = 200
	maxActivityDetail = 2000
	// thinkingExcerpt is how much of a thought is kept in Detail.
	thinkingExcerpt = 300
	// thinkingEmitEvery rate-limits running updates of a growing thought.
	thinkingEmitEvery = 250 * time.Millisecond
)

// Clip shortens s to at most n runes, ending in "…" when it was cut. The
// result is never longer than n runes, and a cut never splits a rune.
func Clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n { // fewer bytes than runes allowed: cannot be too long
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

var activitySeq atomic.Int64

// newActivityID returns an ID unique within the process, so two runs in the
// same node never replace each other's items.
func newActivityID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, activitySeq.Add(1))
}

// oneLine collapses s to a single trimmed line, clipped to maxActivityTitle.
func oneLine(s string) string {
	return Clip(strings.Join(strings.Fields(s), " "), maxActivityTitle)
}

// countLines is the number of lines in s: 0 for "", otherwise newlines plus
// one, not counting a trailing newline.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// thinkingTracker coalesces reasoning deltas into one running Activity that is
// updated (at most every thinkingEmitEvery) and finished with a done update.
type thinkingTracker struct {
	id       string
	head     strings.Builder // first thinkingExcerpt runes
	headRune int
	runes    int
	started  time.Time
	lastEmit time.Time
}

// add records a reasoning delta and returns the Activity to emit, or nil when
// an update was emitted too recently.
func (t *thinkingTracker) add(delta string, now time.Time) *Activity {
	if delta == "" {
		return nil
	}
	first := t.id == ""
	if first {
		t.id = newActivityID("think")
		t.started = now
	}
	for _, r := range delta {
		if t.headRune < thinkingExcerpt {
			t.head.WriteRune(r)
			t.headRune++
		}
		t.runes++
	}
	if !first && now.Sub(t.lastEmit) < thinkingEmitEvery {
		return nil
	}
	t.lastEmit = now
	return t.activity(ActRunning, now)
}

// finish returns the final done Activity, or nil when nothing was thought. The
// tracker is reset so a later thought gets a new ID.
func (t *thinkingTracker) finish(now time.Time) *Activity {
	if t.id == "" {
		return nil
	}
	a := t.activity(ActDone, now)
	*t = thinkingTracker{}
	return a
}

func (t *thinkingTracker) activity(state ActivityState, now time.Time) *Activity {
	a := &Activity{
		ID:     t.id,
		Kind:   ActThinking,
		State:  state,
		Title:  fmt.Sprintf("Thinking (%d chars)", t.runes),
		Detail: t.head.String(),
		At:     now,
	}
	if state != ActRunning {
		a.Duration = now.Sub(t.started)
	}
	return a
}

// emitActivity sends an Activity token.
func emitActivity(ch chan<- Token, a *Activity) {
	if a != nil {
		ch <- Token{Activity: a}
	}
}

// toolInfo is the normalised meaning of one tool call.
type toolInfo struct {
	Kind    ActivityKind
	Title   string
	Detail  string
	Added   int
	Removed int
}

// jsonString reads the first non-empty string value among keys.
func jsonString(m map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(raw, &s) == nil && s != "" {
			return s
		}
	}
	return ""
}

// classifyTool maps a tool name and its JSON input to an Activity kind. It
// understands the tool names and input keys of Claude Code (Read, Edit,
// MultiEdit, Write, Bash, Grep, Glob, WebSearch, WebFetch, Task, ...) and
// OpenCode (read, edit, write, bash/shell, grep, glob, list, webfetch, task,
// patch, ...). Unknown tools become ActTool with the name as Title.
func classifyTool(name string, input json.RawMessage) toolInfo {
	in := map[string]json.RawMessage{}
	_ = json.Unmarshal(input, &in) // a non-object input leaves in empty
	path := jsonString(in, "file_path", "filePath", "path", "notebook_path")
	switch strings.ToLower(name) {
	case "read", "view":
		return toolInfo{Kind: ActRead, Title: oneLine(path)}
	case "write", "create":
		return toolInfo{Kind: ActWrite, Title: oneLine(path), Added: countLines(jsonString(in, "content", "file_text"))}
	case "edit", "str_replace_editor":
		a, r := editCounts(in)
		return toolInfo{Kind: ActEdit, Title: oneLine(path), Added: a, Removed: r}
	case "multiedit":
		var edits []map[string]json.RawMessage
		_ = json.Unmarshal(in["edits"], &edits)
		a, r := 0, 0
		for _, e := range edits {
			ea, er := editCounts(e)
			a, r = a+ea, r+er
		}
		return toolInfo{Kind: ActEdit, Title: oneLine(path), Added: a, Removed: r}
	case "notebookedit":
		return toolInfo{Kind: ActEdit, Title: oneLine(path), Added: countLines(jsonString(in, "new_source"))}
	case "patch", "apply_patch":
		return toolInfo{Kind: ActEdit, Title: "patch"}
	case "bash", "shell", "exec_command", "run_shell_command":
		return toolInfo{Kind: ActCommand, Title: oneLine(jsonString(in, "command", "cmd")), Detail: Clip(jsonString(in, "description"), maxActivityDetail)}
	case "grep", "glob", "list", "ls", "search", "find", "codesearch":
		q := jsonString(in, "pattern", "query", "path")
		return toolInfo{Kind: ActSearch, Title: oneLine(q)}
	case "websearch", "web_search":
		return toolInfo{Kind: ActSearch, Title: oneLine(jsonString(in, "query"))}
	case "webfetch", "web_fetch":
		return toolInfo{Kind: ActSearch, Title: oneLine(jsonString(in, "url"))}
	case "task", "agent", "subagent":
		d := jsonString(in, "description", "subagent_type", "agent")
		return toolInfo{Kind: ActTool, Title: oneLine(name + ": " + d), Detail: Clip(jsonString(in, "prompt"), maxActivityDetail)}
	}
	title := name
	if title == "" {
		title = "tool"
	}
	return toolInfo{Kind: ActTool, Title: oneLine(title)}
}

// editCounts is (added, removed) lines of one old/new text replacement.
func editCounts(in map[string]json.RawMessage) (added, removed int) {
	return countLines(jsonString(in, "new_string", "newString")), countLines(jsonString(in, "old_string", "oldString"))
}
