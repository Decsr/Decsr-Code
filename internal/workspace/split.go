package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/prompts"
	"github.com/Decsr/Decsr-Code/internal/runner"
)

const (
	markerTitle    = "===TITLE==="
	markerTasks    = "===TASKS==="
	markerNewRoles = "===NEW_ROLES==="
	markerEnd      = "===END==="

	// maxSplitTasks is the most sub-tasks the lead may produce.
	maxSplitTasks = 12
)

// reservedKeyRe matches the IDs the system creates itself: the check node and
// fix nodes.
var reservedKeyRe = regexp.MustCompile(`^(CHECK|F[0-9]+)$`)

// BuildSplitPrompt assembles the lead's system prompt.
func BuildSplitPrompt(task string, roles []Role, projectContext string) string {
	return buildSplitPrompt(task, roles, projectContext, nil, 0)
}

// followUpRules is added to the prompt when the graph already has nodes.
func followUpRules(existing []string, next int) string {
	if len(existing) == 0 {
		return ""
	}
	if next < 1 {
		next = nextAfter(existing)
	}
	return fmt.Sprintf("\n## Follow-up task\n\n"+
		"The graph already has these nodes: %s.\n"+
		"- Number the new sub-tasks from `T%d` upward. Never reuse an existing ID.\n"+
		"- You MAY list an existing ID in `depends_on` when the new work needs that node's result.\n",
		strings.Join(existing, ", "), next)
}

// nextAfter is one more than the highest T<number> among ids (1 for none).
func nextAfter(ids []string) int {
	top := 0
	for _, id := range ids {
		if len(id) > 1 && id[0] == 'T' {
			if v, err := strconv.Atoi(id[1:]); err == nil && v > top {
				top = v
			}
		}
	}
	return top + 1
}

func buildSplitPrompt(task string, roles []Role, projectContext string, existing []string, next int) string {
	var sb strings.Builder
	for _, r := range roles {
		if r.ReadOnly {
			fmt.Fprintf(&sb, "- %s: %s (read-only: reviews and reports, cannot change files)\n", r.Name, r.Description)
			continue
		}
		fmt.Fprintf(&sb, "- %s: %s\n", r.Name, r.Description)
	}
	p := prompts.MustLoad("ws-split")
	// One pass, so placeholder text inside a value is never substituted.
	return strings.NewReplacer(
		"{{ROLES}}", strings.TrimRight(sb.String(), "\n"),
		"{{FOLLOWUP}}", followUpRules(existing, next),
		"{{TASK}}", task,
		"{{PROJECT_CONTEXT}}", projectContext,
	).Replace(p)
}

type splitTask struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Role        string   `json:"role"`
	DependsOn   []string `json:"depends_on"`
}

// UnmarshalJSON accepts the common alternative key names the lead uses; an
// alias is only read when the canonical key is absent.
func (t *splitTask) UnmarshalJSON(data []byte) error {
	var raw struct {
		Key          string   `json:"key"`
		ID           string   `json:"id"`
		Title        string   `json:"title"`
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		Role         string   `json:"role"`
		Agent        string   `json:"agent"`
		Assignee     string   `json:"assignee"`
		Owner        string   `json:"owner"`
		DependsOn    []string `json:"depends_on"`
		Depends      []string `json:"depends"`
		Dependencies []string `json:"dependencies"`
		DependsOnC   []string `json:"dependsOn"`
		Deps         []string `json:"deps"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	first := func(vs ...string) string {
		for _, v := range vs {
			if strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	t.Key = first(raw.Key, raw.ID)
	t.Title = first(raw.Title, raw.Name)
	t.Description = raw.Description
	t.Role = first(raw.Role, raw.Agent, raw.Assignee, raw.Owner)
	t.DependsOn = raw.DependsOn
	for _, alt := range [][]string{raw.Depends, raw.Dependencies, raw.DependsOnC, raw.Deps} {
		if t.DependsOn == nil {
			t.DependsOn = alt
		}
	}
	return nil
}

// ParseSplit turns the lead's output into a validated graph. Roles named by
// the lead become suggestions only; nothing is assigned.
func ParseSplit(task, output string, roles []Role) (*Graph, error) {
	return ParseSplitWith(task, output, roles, nil)
}

// ParseSplitWith is ParseSplit for a follow-up task: a dependency that names
// one of the existing node IDs (and not one of the new tasks' own keys) is
// kept as an external dependency and passes validation.
func ParseSplitWith(task, output string, roles []Role, existing []string) (*Graph, error) {
	tasksRaw, ok := section(output, markerTasks)
	if !ok {
		return nil, fmt.Errorf("split: no %s section in the lead's answer", markerTasks)
	}
	var tasks []splitTask
	if err := unmarshalArray(tasksRaw, &tasks); err != nil {
		return nil, fmt.Errorf("split: the lead's answer was not valid JSON (%s)", shortReason(err))
	}
	if len(tasks) == 0 {
		return nil, fmt.Errorf("split: the lead returned no tasks")
	}
	if len(tasks) > maxSplitTasks {
		return nil, fmt.Errorf("split: too many sub-tasks (%d, max %d)", len(tasks), maxSplitTasks)
	}

	g := &Graph{Task: task}
	if title, ok := section(output, markerTitle); ok {
		g.Title = Sanitize(strings.SplitN(strings.TrimSpace(title), "\n", 2)[0])
	}
	if g.Title == "" {
		if r := []rune(strings.TrimSpace(task)); len(r) > 60 {
			g.Title = string(r[:60])
		} else {
			g.Title = string(r)
		}
	}

	known := map[string]bool{}
	for _, r := range roles {
		known[r.Name] = true
	}
	declared := map[string]string{} // lead-declared new roles: name -> description
	var declaredOrder []string
	if raw, ok := section(output, markerNewRoles); ok {
		var nr []NewRole
		if err := unmarshalArray(raw, &nr); err == nil {
			for _, r := range nr {
				name := splitRole(r.Name, known)
				if name == "" {
					continue
				}
				if _, dup := declared[name]; !dup {
					declaredOrder = append(declaredOrder, name)
				}
				declared[name] = Sanitize(r.Description)
			}
		}
	}

	ids := splitIDs(tasks)

	proposed := map[string]bool{}
	var proposedOrder []string
	for i, t := range tasks {
		key := upperKey(t.Key)
		nd := &Node{
			ID:          ids.final[i],
			Title:       Sanitize(t.Title),
			Description: strings.TrimSpace(SanitizeText(t.Description)),
			Kind:        KindWork,
			Status:      StatusTodo,
		}
		if nd.Title == "" {
			nd.Title = key
		}
		if nd.Description == "" {
			nd.Description = nd.Title
		}
		for _, d := range t.DependsOn {
			d = upperKey(d)
			if renamed, ok := ids.renamed[d]; ok {
				d = renamed
			}
			nd.DependsOn = append(nd.DependsOn, d)
		}
		if role := splitRole(t.Role, known); role != "" {
			nd.SuggestedRole = role
			if !known[role] && !proposed[role] {
				proposed[role] = true
				proposedOrder = append(proposedOrder, role)
			}
		}
		g.Nodes = append(g.Nodes, nd)
	}
	// Declared roles first (in the lead's order), then undeclared ones used by nodes.
	emitted := map[string]bool{}
	for _, name := range declaredOrder {
		if known[name] || emitted[name] {
			continue
		}
		emitted[name] = true
		g.NewRoles = append(g.NewRoles, NewRole{Name: name, Description: declared[name]})
	}
	for _, name := range proposedOrder {
		if !emitted[name] {
			emitted[name] = true
			g.NewRoles = append(g.NewRoles, NewRole{Name: name})
		}
	}

	// External dependencies are checked apart: Validate only knows g's nodes.
	own := make(map[string]bool, len(g.Nodes))
	for _, nd := range g.Nodes {
		own[nd.ID] = true
	}
	outside := map[string]bool{}
	for _, id := range existing {
		if id != "" && id != "CHECK" && !own[id] {
			outside[id] = true
		}
	}
	external := make(map[*Node][]string)
	for _, nd := range g.Nodes {
		var internal []string
		for _, d := range nd.DependsOn {
			if outside[d] {
				external[nd] = append(external[nd], d)
			} else {
				internal = append(internal, d)
			}
		}
		nd.DependsOn = internal
	}
	err := g.Validate()
	for _, nd := range g.Nodes {
		nd.DependsOn = append(nd.DependsOn, external[nd]...)
	}
	if err != nil {
		return nil, fmt.Errorf("split: %v", err)
	}
	return g, nil
}

const splitRetryMsg = `Your answer is missing the "role" (and possibly "depends_on") fields. Output the complete answer again in the exact format, with a role for every sub-task and depends_on wherever one sub-task needs another's files.`

// Split asks the lead to split task and parses the answer. When the graph has
// several nodes and the lead suggested no role at all, it asks once more.
func Split(ctx context.Context, r runner.LLMRunner, model string, task string, roles []Role, projectContext string, onToken func(string)) (*Graph, runner.TokenUsage, error) {
	return SplitWith(ctx, r, model, task, roles, projectContext, nil, onToken)
}

// SplitWith is Split for a follow-up task: existing lists the IDs already in
// the graph, which the lead may depend on and must not reuse. New sub-tasks
// are numbered after the highest existing T number.
func SplitWith(ctx context.Context, r runner.LLMRunner, model string, task string, roles []Role, projectContext string, existing []string, onToken func(string)) (*Graph, runner.TokenUsage, error) {
	return SplitNext(ctx, r, model, task, roles, projectContext, existing, 0, onToken)
}

// SplitNext is SplitWith with an explicit first number for the new sub-tasks
// (0 = after the highest existing T number).
func SplitNext(ctx context.Context, r runner.LLMRunner, model string, task string, roles []Role, projectContext string, existing []string, next int, onToken func(string)) (*Graph, runner.TokenUsage, error) {
	req := runner.CompletionRequest{
		SystemPrompt: buildSplitPrompt(task, roles, projectContext, existing, next),
		Model:        model,
		Messages:     []runner.ConvMessage{{Role: "user", Content: task}},
	}
	raw, usage, err := splitComplete(ctx, r, req, onToken)
	if err != nil {
		return nil, usage, err
	}
	g, err := ParseSplitWith(task, raw, roles, existing)
	if err != nil {
		return nil, usage, err
	}
	if len(g.Nodes) < 2 || anySuggestedRole(g) {
		return g, usage, nil
	}
	req.Messages = append(append([]runner.ConvMessage(nil), req.Messages...),
		runner.ConvMessage{Role: "assistant", Content: raw},
		runner.ConvMessage{Role: "user", Content: splitRetryMsg})
	raw2, usage2, err := splitComplete(ctx, r, req, onToken)
	usage.InputTokens += usage2.InputTokens
	usage.OutputTokens += usage2.OutputTokens
	usage.Estimated = usage.Estimated || usage2.Estimated
	if err != nil {
		if ctx.Err() != nil {
			return nil, usage, err
		}
		return g, usage, nil
	}
	if g2, err := ParseSplitWith(task, raw2, roles, existing); err == nil && anySuggestedRole(g2) {
		return g2, usage, nil
	}
	return g, usage, nil
}

func anySuggestedRole(g *Graph) bool {
	for _, n := range g.Nodes {
		if n.SuggestedRole != "" {
			return true
		}
	}
	return false
}

// splitComplete runs one completion and returns the whole answer, streaming
// it to onToken.
func splitComplete(ctx context.Context, r runner.LLMRunner, req runner.CompletionRequest, onToken func(string)) (string, runner.TokenUsage, error) {
	var usage runner.TokenUsage
	ch, err := r.Complete(ctx, req)
	if err != nil {
		return "", usage, fmt.Errorf("split: runner: %w", err)
	}
	var sb strings.Builder
	for tok := range ch {
		if tok.Error != nil {
			return "", usage, fmt.Errorf("split: stream: %w", tok.Error)
		}
		// Usage snapshots from streaming runners are cumulative: keep the latest.
		if tok.Usage != nil {
			usage = *tok.Usage
		}
		if tok.Text != "" {
			sb.WriteString(tok.Text)
			if onToken != nil {
				onToken(tok.Text)
			}
		}
		if tok.Done {
			break
		}
	}
	return sb.String(), usage, nil
}

// section returns the text after the last occurrence of marker up to the
// next known marker or the end.
func section(s, marker string) (string, bool) {
	i := strings.LastIndex(s, marker)
	if i < 0 {
		return "", false
	}
	rest := s[i+len(marker):]
	end := len(rest)
	for _, m := range []string{markerTitle, markerTasks, markerNewRoles, markerEnd} {
		if j := strings.Index(rest, m); j >= 0 && j < end {
			end = j
		}
	}
	return strings.TrimSpace(rest[:end]), true
}

func upperKey(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

type splitKeys struct {
	final   []string          // final ID per task, in task order
	renamed map[string]string // reserved key -> replacement
}

// splitIDs upper-cases the task keys and renames reserved ones (CHECK, F<n>)
// to a unique non-reserved ID such as T_CHECK.
func splitIDs(tasks []splitTask) splitKeys {
	used := map[string]bool{}
	for _, t := range tasks {
		used[upperKey(t.Key)] = true
	}
	k := splitKeys{renamed: map[string]string{}}
	for _, t := range tasks {
		id := upperKey(t.Key)
		if reservedKeyRe.MatchString(id) {
			cand := "T_" + id
			for n := 2; used[cand]; n++ {
				cand = "T_" + id + "_" + strconv.Itoa(n)
			}
			used[cand] = true
			if _, seen := k.renamed[id]; !seen {
				k.renamed[id] = cand
			}
			id = cand
		}
		k.final = append(k.final, id)
	}
	return k
}

// unmarshalArray decodes the first JSON array found in raw: the span from a
// "[" to its matching "]", so prose or a code fence around it is ignored.
// Trailing commas are tolerated. Later "[" are tried when an earlier one is
// not the array.
func unmarshalArray(raw string, v any) error {
	var firstErr error
	for start := 0; start < len(raw); start++ {
		if raw[start] != '[' {
			continue
		}
		end := matchBracket(raw, start)
		if end < 0 {
			if firstErr == nil {
				firstErr = fmt.Errorf("unterminated array")
			}
			continue
		}
		err := json.Unmarshal([]byte(dropTrailingCommas(raw[start:end+1])), v)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("no JSON array found")
	}
	return firstErr
}

// matchBracket returns the index of the "]" matching the "[" at start,
// skipping strings, or -1.
func matchBracket(s string, start int) int {
	depth := 0
	inStr := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// dropTrailingCommas removes a comma that is followed only by whitespace and
// a closing "]" or "}", outside strings.
func dropTrailingCommas(s string) string {
	var sb strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			sb.WriteByte(c)
			switch c {
			case '\\':
				if i+1 < len(s) {
					i++
					sb.WriteByte(s[i])
				}
			case '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
		} else if c == ',' {
			j := i + 1
			for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n' || s[j] == '\r') {
				j++
			}
			if j < len(s) && (s[j] == ']' || s[j] == '}') {
				continue
			}
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

func shortReason(err error) string {
	r := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(r) > 100 {
		r = r[:100] + "..."
	}
	return r
}

// splitRole is normalizeRole, except that the exact name of a known plugin
// role ("<plugin>:<agent>") is kept as it is.
func splitRole(s string, known map[string]bool) string {
	if name := strings.TrimSpace(s); config.ValidQualifiedName(name) && known[name] {
		return name
	}
	return normalizeRole(s)
}

// normalizeRole lower-cases a role name, maps characters outside [a-z0-9_]
// to underscores and fixes it up to satisfy config.ValidRoleName: "r_" is
// prefixed when it does not start with a letter, a one-character name is
// padded and a long one is trimmed to 32 characters. "" stays "".
func normalizeRole(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, s)
	if s[0] < 'a' || s[0] > 'z' {
		s = "r_" + s
	}
	if len(s) < 2 {
		s += "_"
	}
	if len(s) > 32 {
		s = s[:32]
	}
	if !config.ValidRoleName(s) { // unreachable safety net
		return "r_role"
	}
	return s
}
