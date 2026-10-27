package decsr

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Decsr/Decsr-Code/internal/agent"
	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
	appctx "github.com/Decsr/Decsr-Code/internal/context"
	"github.com/Decsr/Decsr-Code/internal/contextpack"
	"github.com/Decsr/Decsr-Code/internal/tokenutil"
)

const (
	defaultContextPackTokens    = 6000
	maxCompactSourceChars       = 30000
	maxCompactPerFileChars      = 4000
	maxCompactAlwaysIncludeChar = 3000
)

// contextPackEnabled reports whether the context-first phase is active.
func (tr *TaskRunner) contextPackEnabled() bool {
	return !tr.cfg.Project.Context.Pack.Disable
}

// loadContextPack returns the approved context pack, if one exists.
func (tr *TaskRunner) loadContextPack() (string, bool) {
	if _, err := tr.ws.ReadFile(artifacts.ContextPackApprovedFile); err != nil {
		return "", false
	}
	data, err := tr.ws.ReadFile(artifacts.ContextPackFile)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return "", false
	}
	return string(data), true
}

// runContextPackGate generates the repository context pack and waits for the
// human to edit/approve it. In non-interactive runs the pack is auto-approved.
func (tr *TaskRunner) runContextPackGate(ctx context.Context, pc appctx.ProjectContext) (string, error) {
	maxTokens := tr.cfg.Project.Context.Pack.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = defaultContextPackTokens
	}

	// A new pack invalidates any previous approval.
	_ = removeWorkspaceFile(tr.ws, artifacts.ContextPackApprovedFile)

	pm, hasPM := tr.agents[bus.RolePM].(*agent.PMAgent)

	for {
		tr.setState(PipelineCompacting)
		tr.event("PM compacting repository context…")

		// Preserve the user-editable block from a previous version and show it
		// to the PM so the regenerated pack stays consistent with it.
		var existingBlock string
		if data, err := tr.ws.ReadFile(artifacts.ContextPackFile); err == nil {
			existingBlock, _ = contextpack.ExtractUserBlock(string(data))
		}

		input := buildCompactionInput(pc, tr.cfg)
		if existingBlock != "" {
			input += "\n\n### Existing user context (preserve its intent)\n\n" + existingBlock + "\n"
		}

		var generated string
		if hasPM {
			var err error
			generated, err = pm.CompactRepository(ctx, input, maxTokens)
			if err != nil {
				tr.event(fmt.Sprintf("warning: context pack generation: %v — using deterministic fallback", err))
				generated = buildFallbackContextPack(pc, tr.cfg, tr.root)
			}
		} else {
			tr.event("no PM agent — using deterministic context pack")
			generated = buildFallbackContextPack(pc, tr.cfg, tr.root)
		}

		generated = contextpack.EnsureUserBlock(generated)
		generated = capContextPackGenerated(generated, maxTokens)
		if existingBlock != "" {
			generated, _ = contextpack.ReplaceUserBlock(generated, existingBlock)
		}

		if err := tr.ws.WriteFile(artifacts.ContextPackFile, []byte(generated)); err != nil {
			return "", fmt.Errorf("write context pack: %w", err)
		}
		tr.event("context pack ready — review, add your context, and approve")

		if !tr.interactive {
			tr.markContextPackApproved()
			tr.event(fmt.Sprintf("context pack auto-approved: %s", tr.ws.Path(artifacts.ContextPackFile)))
			tr.memAppend("context-pack", "Repository context pack generated and auto-approved (plain mode).")
			return generated, nil
		}

		approved, err := tr.waitArtifact(ctx, artifacts.ContextPackFile)
		if err != nil {
			return "", err
		}
		if approved {
			data, err := tr.ws.ReadFile(artifacts.ContextPackFile)
			if err != nil {
				return "", fmt.Errorf("read approved context pack: %w", err)
			}
			content := string(data)
			if strings.TrimSpace(content) == "" {
				return "", fmt.Errorf("approved context pack is empty")
			}
			// Keep the user block markers intact even if the user removed them
			// while editing; regeneration relies on them to preserve additions.
			if ensured := contextpack.EnsureUserBlock(content); ensured != content {
				content = ensured
				if werr := tr.ws.WriteFile(artifacts.ContextPackFile, []byte(content)); werr != nil {
					tr.event(fmt.Sprintf("warning: rewrite context pack markers: %v", werr))
				}
			}
			tr.markContextPackApproved()
			tr.memAppend("context-pack", "Repository context pack approved.")
			return content, nil
		}

		tr.event("regenerating context pack…")
	}
}

func (tr *TaskRunner) markContextPackApproved() {
	if err := tr.ws.WriteFile(artifacts.ContextPackApprovedFile, []byte("approved\n")); err != nil {
		tr.event(fmt.Sprintf("warning: write context pack approval marker: %v", err))
	}
}

// askIntent asks the human what they want to do after the repository context
// has been approved. In plain mode the supplied task description is used.
func (tr *TaskRunner) askIntent(ctx context.Context, initial string) (string, error) {
	tr.setState(PipelineNegotiating)
	tr.b.Publish(bus.NewMessage(bus.RolePM, "", bus.MsgConversation, bus.ConversationPayload{
		From:    "pm",
		Content: "I reviewed and compacted the repository. What would you like me to do?",
	}))

	if !tr.interactive {
		initial = strings.TrimSpace(initial)
		if initial == "" {
			return "", fmt.Errorf("no task description provided")
		}
		return initial, nil
	}

	for {
		reply, ok := tr.waitHuman(ctx)
		if !ok {
			return "", ctx.Err()
		}
		if strings.TrimSpace(reply) != "" {
			return strings.TrimSpace(reply), nil
		}
	}
}

// buildContextFragment returns the context injected into planning and review
// agents. When a pack is available it becomes the primary context, with a
// small deterministic facts block and project memory appended.
func (tr *TaskRunner) buildContextFragment(pc appctx.ProjectContext) string {
	if tr.contextPack == "" {
		return pc.SystemPromptFragment(appctx.ProfileFull)
	}

	var sb strings.Builder
	sb.WriteString("## Approved Repository Context Pack\n\n")
	sb.WriteString(strings.TrimSpace(tr.contextPack))
	sb.WriteString("\n\n## Repository Facts (generated)\n\n")

	if pc.ProjectType != "" {
		fmt.Fprintf(&sb, "- Project type: %s\n", pc.ProjectType)
	}
	if pc.ProgrammingLanguage != "" {
		fmt.Fprintf(&sb, "- Primary language: %s\n", pc.ProgrammingLanguage)
	}
	if pc.IsBrownfield {
		sb.WriteString("- Existing codebase: yes — modify existing structures; do not scaffold a parallel project\n")
	}
	if tr.cfg.Project.TestCmd != "" {
		fmt.Fprintf(&sb, "- Test command: `%s`\n", tr.cfg.Project.TestCmd)
	}
	if tr.cfg.Project.LintCmd != "" {
		fmt.Fprintf(&sb, "- Lint command: `%s`\n", tr.cfg.Project.LintCmd)
	}

	if pc.TreeStructure != "" {
		sb.WriteString("\n### Project Structure\n```\n")
		sb.WriteString(pc.TreeStructure)
		sb.WriteString("\n```\n")
	}

	if frag := pc.Memory.PromptFragment(); frag != "" {
		sb.WriteString("\n")
		sb.WriteString(frag)
	}

	return sb.String()
}

// buildCompactionInput assembles the repository material sent to the PM for
// compaction. It deliberately prefers a compact profile plus key source files
// over the full raw context, keeping the prompt within local-model budgets.
func buildCompactionInput(pc appctx.ProjectContext, cfg config.Config) string {
	var sb strings.Builder
	sb.WriteString(pc.SystemPromptFragment(appctx.ProfileCompact))

	sb.WriteString("\n### Build / Test / Lint Commands\n\n")
	if cfg.Project.TestCmd != "" {
		fmt.Fprintf(&sb, "- Test: `%s`\n", cfg.Project.TestCmd)
	}
	if cfg.Project.LintCmd != "" {
		fmt.Fprintf(&sb, "- Lint: `%s`\n", cfg.Project.LintCmd)
	}
	if cfg.Project.TestCmd == "" && cfg.Project.LintCmd == "" {
		sb.WriteString("- None configured\n")
	}

	sb.WriteString("\n### Tracked Files\n```\n")
	files := pc.Files
	if len(files) > 150 {
		files = files[:150]
	}
	sb.WriteString(strings.Join(files, "\n"))
	sb.WriteString("\n```\n")

	if len(pc.RecentCommits) > 0 {
		sb.WriteString("\n### Recent Commits\n```\n")
		commits := pc.RecentCommits
		if len(commits) > 10 {
			commits = commits[:10]
		}
		sb.WriteString(strings.Join(commits, "\n"))
		sb.WriteString("\n```\n")
	}

	if len(pc.AlwaysInclude) > 0 {
		sb.WriteString("\n### Always-Included Files\n\n")
		for _, name := range sortedMapKeys(pc.AlwaysInclude) {
			content := pc.AlwaysInclude[name]
			if len(content) > maxCompactAlwaysIncludeChar {
				content = content[:maxCompactAlwaysIncludeChar] + "\n... (truncated)"
			}
			fmt.Fprintf(&sb, "**%s**\n```\n%s\n```\n\n", name, content)
		}
	}

	if len(pc.SourceFiles) > 0 {
		sb.WriteString("\n### Existing Source Files\n\n")
		total := 0
		for _, name := range sortedMapKeys(pc.SourceFiles) {
			if total >= maxCompactSourceChars {
				break
			}
			content := pc.SourceFiles[name]
			if len(content) > maxCompactPerFileChars {
				content = content[:maxCompactPerFileChars] + "\n... (truncated)"
			}
			if total+len(content) > maxCompactSourceChars {
				break
			}
			fmt.Fprintf(&sb, "**%s**\n```\n%s\n```\n\n", name, content)
			total += len(content)
		}
	}

	return sb.String()
}

// buildFallbackContextPack produces a usable pack when the PM call fails.
func buildFallbackContextPack(pc appctx.ProjectContext, cfg config.Config, root string) string {
	name := cfg.Project.Name
	if name == "" {
		name = filepath.Base(root)
	}

	var sb strings.Builder
	sb.WriteString("# Repository Context Pack\n\n")
	fmt.Fprintf(&sb, "> Generated deterministically on %s (PM compaction unavailable).\n\n", time.Now().Format("2006-01-02"))

	sb.WriteString("## 1. Project Snapshot\n\n")
	fmt.Fprintf(&sb, "- Name: %s\n", name)
	if cfg.Project.Language != "" {
		fmt.Fprintf(&sb, "- Language: %s\n", cfg.Project.Language)
	}
	if pc.ProjectType != "" {
		fmt.Fprintf(&sb, "- Project type: %s\n", pc.ProjectType)
	}
	if pc.IsBrownfield {
		sb.WriteString("- Existing codebase: yes\n")
	}
	if cfg.Project.TestCmd != "" {
		fmt.Fprintf(&sb, "- Test command: `%s`\n", cfg.Project.TestCmd)
	}
	if cfg.Project.LintCmd != "" {
		fmt.Fprintf(&sb, "- Lint command: `%s`\n", cfg.Project.LintCmd)
	}

	sb.WriteString("\n## 2. Architecture at a Glance\n\n")
	sb.WriteString("_Not inferred — inspect the key code map and source files._\n")

	sb.WriteString("\n## 3. Key Code Map\n\n")
	limit := 80
	if len(pc.Files) < limit {
		limit = len(pc.Files)
	}
	for _, f := range pc.Files[:limit] {
		fmt.Fprintf(&sb, "- `%s`\n", f)
	}

	if pc.TreeStructure != "" {
		sb.WriteString("\n## 4. Project Structure\n```\n")
		sb.WriteString(pc.TreeStructure)
		sb.WriteString("\n```\n")
	}

	if frag := pc.Memory.PromptFragment(); frag != "" {
		sb.WriteString("\n## 5. Project Memory\n\n")
		sb.WriteString(frag)
	}

	sb.WriteString("\n## 6. Constraints, Risks & Unknowns\n\n")
	sb.WriteString("_Generated fallback — add anything the PM could not infer._\n")

	sb.WriteString("\n" + contextpack.UserHeading + "\n\n")
	sb.WriteString(contextpack.DefaultUserBlock)
	sb.WriteString("\n\n## 8. Open Questions\n\n")
	sb.WriteString("_Generated fallback — add missing context above._\n")

	return sb.String()
}

// capContextPackGenerated truncates the generated portion of a pack while
// preserving the user-editable block and any content after it.
func capContextPackGenerated(doc string, maxTokens int) string {
	if maxTokens <= 0 {
		maxTokens = defaultContextPackTokens
	}
	block, hasBlock := contextpack.ExtractUserBlock(doc)
	if !hasBlock {
		body := strings.TrimRight(doc, "\n")
		if tokenutil.EstimateTokens(body) > maxTokens {
			body = tokenutil.Truncate(body, maxTokens)
		}
		return body
	}

	start := strings.Index(doc, contextpack.BeginMarker)
	if start < 0 {
		return strings.TrimRight(doc, "\n")
	}
	end := start + len(block)
	before := strings.TrimRight(doc[:start], "\n")
	after := strings.TrimSpace(doc[end:])

	if tokenutil.EstimateTokens(before) > maxTokens {
		before = tokenutil.Truncate(before, maxTokens)
	}

	result := before + "\n\n" + block
	if after != "" {
		result += "\n\n" + after
	}
	return result
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
