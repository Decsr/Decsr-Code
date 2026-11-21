package workspace

import (
	"regexp"
	"strings"
)

const markerAsk = "===ASK==="

const (
	minAskOptions = 2
	maxAskOptions = 4
)

// LeadAsk is a question the lead asks the user, with the options to choose
// from. Recommended is the 1-based number of the recommended option; 0 none.
type LeadAsk struct {
	Question    string
	Options     []string
	Recommended int
}

var recommendedRe = regexp.MustCompile(`(?i)\s*\(recommended\)`)

// parseAsk reads the ASK section: the question on the first lines, then one
// option per line starting with "- ". An option marked "(recommended)" is the
// recommended one. It returns nil when there is no section, no question or
// fewer than two options.
func parseAsk(output string) *LeadAsk {
	raw, ok := section(output, markerAsk)
	if !ok {
		return nil
	}
	a := &LeadAsk{}
	var question []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* "):
			if len(a.Options) == maxAskOptions {
				continue
			}
			opt := strings.TrimSpace(line[2:])
			if recommendedRe.MatchString(opt) {
				opt = strings.TrimSpace(recommendedRe.ReplaceAllString(opt, ""))
				if a.Recommended == 0 {
					a.Recommended = len(a.Options) + 1
				}
			}
			if opt != "" {
				a.Options = append(a.Options, opt)
			}
		case line != "" && len(a.Options) == 0:
			question = append(question, line)
		}
	}
	a.Question = strings.Join(question, " ")
	if a.Question == "" || len(a.Options) < minAskOptions {
		return nil
	}
	if a.Recommended > len(a.Options) {
		a.Recommended = 0
	}
	return a
}
