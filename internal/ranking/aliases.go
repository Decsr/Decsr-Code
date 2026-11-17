package ranking

import (
	"regexp"
	"strings"
)

// builtinAliases map a normalized AA name to the runner model ids. Users add
// to or replace them with routing.aliases.
var builtinAliases = map[string][]Target{
	"claude opus 5.5":     {{"claude", "claude-opus-5-5"}},
	"claude sonnet 5.5":   {{"claude", "claude-sonnet-5-5"}},
	"claude haiku 5.5":    {{"claude", "claude-haiku-5-5"}},
	"claude fable 5.2":    {{"claude", "claude-fable-5-2"}},
	"deepseek v4.1 flash": {{"dsh", "deepseek-flash"}, {"deepseek", "deepseek-flash"}},
	"deepseek v4 flash":   {{"dsh", "deepseek-v4-flash"}, {"deepseek", "deepseek-v4-flash"}},
	"deepseek v4 pro":     {{"dsh", "deepseek-v4-pro"}, {"deepseek", "deepseek-v4-pro"}},
	"gpt 5.5":             {{"codex", "gpt-5.5"}, {"openai", "gpt-5.5"}},
	"gpt 5.4":             {{"codex", "gpt-5.4"}, {"openai", "gpt-5.4"}},
	"gpt 5.4 mini":        {{"codex", "gpt-5.4-mini"}, {"openai", "gpt-5.4-mini"}},
}

var (
	nonAlnum    = regexp.MustCompile(`[^a-z0-9.]+`)
	parenRe     = regexp.MustCompile(`\(([^)]*)\)`)
	effortWord  = regexp.MustCompile(`\b(x-?high|max|high|medium|low|minimal)\b`)
	nonReason   = regexp.MustCompile(`non[- ]?reasoning`)
	slugEffort  = regexp.MustCompile(`-(xhigh|x-high|max|high|medium|low|minimal|non-reasoning)$`)
	spaceRuns   = regexp.MustCompile(`\s+`)
	trailingSep = regexp.MustCompile(`[\s,:-]+$`)
)

// Normalize makes names comparable: lowercase, letters, digits and dots, one
// space between words ("Claude Opus 5.5" is "claude opus 5.5").
func Normalize(s string) string {
	return strings.TrimSpace(nonAlnum.ReplaceAllString(strings.ToLower(s), " "))
}

func canonEffort(w string) string {
	switch strings.ReplaceAll(w, "-", "") {
	case "xhigh":
		return "xhigh"
	case "nonreasoning":
		return "off"
	}
	return w
}

// ParseEffort splits an AA name and slug into the model name without its
// effort and the effort: "Claude Opus 5.5 (Max)" gives ("Claude Opus 5.5",
// "max"); a "non-reasoning" entry gives "off". Without an effort in the name,
// a slug suffix such as "-high" is used. effort is "" when none is found.
func ParseEffort(name, slug string) (base, effort string) {
	base = strings.TrimSpace(name)
	for _, loc := range parenRe.FindAllStringSubmatchIndex(name, -1) {
		inner := strings.ToLower(name[loc[2]:loc[3]])
		w := effortWord.FindString(inner)
		if w == "" && nonReason.MatchString(inner) {
			w = "non-reasoning"
		}
		if w != "" {
			effort = canonEffort(w)
			base = spaceRuns.ReplaceAllString(strings.TrimSpace(name[:loc[0]]+" "+name[loc[1]:]), " ")
			base = trailingSep.ReplaceAllString(base, "")
			return base, effort
		}
	}
	if m := slugEffort.FindStringSubmatch(strings.ToLower(slug)); m != nil {
		return base, canonEffort(m[1])
	}
	return base, ""
}
