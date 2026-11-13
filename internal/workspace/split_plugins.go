package workspace

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/plugins"
)

const (
	markerPlugins = "===PLUGINS==="

	// maxPluginRequests is the most plugins the lead may ask for in one split.
	maxPluginRequests = 5
	// maxSkillLines caps the "Installed skills" section of the split prompt.
	maxSkillLines = 60
	// maxSkillDescRunes cuts a skill description in the split prompt.
	maxSkillDescRunes = 120
	// maxCatalogSkills is the most skills named on one catalog line.
	maxCatalogSkills = 12
	// maxReasonRunes cuts the reason of a plugin request.
	maxReasonRunes = 200
)

// SplitOptions tells the lead which plugin skills exist: the installed
// plugins and the catalog it may suggest from. The zero value offers none.
type SplitOptions struct {
	Plugins plugins.Set
	Catalog []plugins.CatalogEntry
}

func cutRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return s
}

// skillsSection is the body of "Installed skills": the skills of the enabled
// plugins, one line each, at most maxSkillLines.
func skillsSection(set plugins.Set) string {
	var lines []string
	for _, p := range set.Plugins {
		if !p.Enabled {
			continue
		}
		for _, s := range p.Skills {
			line := "- " + s.ID
			if d := cutRunes(Sanitize(s.Description), maxSkillDescRunes); d != "" {
				line += ": " + d
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "(none installed)"
	}
	if len(lines) > maxSkillLines {
		more := len(lines) - maxSkillLines
		lines = append(lines[:maxSkillLines], fmt.Sprintf("- … and %d more", more))
	}
	return strings.Join(lines, "\n")
}

// catalogSection is the body of "Plugins you can suggest": the catalog
// plugins that are not installed, or installed but disabled.
func catalogSection(set plugins.Set, cat []plugins.CatalogEntry) string {
	var lines []string
	for _, e := range cat {
		state := set.State(e.Name)
		if state == plugins.StateEnabled {
			continue
		}
		line := "- " + e.Name
		if state == plugins.StateDisabled {
			line += " (disabled)"
		}
		line += ": " + Sanitize(e.Description)
		if len(e.Skills) > 0 {
			names := e.Skills
			tail := ""
			if len(names) > maxCatalogSkills {
				names, tail = names[:maxCatalogSkills], ", …"
			}
			line += " Skills: " + strings.Join(names, ", ") + tail + "."
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "(none)"
	}
	return strings.Join(lines, "\n")
}

type idList []string

// UnmarshalJSON accepts a list of strings or one string; anything else gives
// an empty list instead of failing the whole answer.
func (l *idList) UnmarshalJSON(data []byte) error {
	var many []string
	if json.Unmarshal(data, &many) == nil {
		*l = many
		return nil
	}
	var one string
	if json.Unmarshal(data, &one) == nil && strings.TrimSpace(one) != "" {
		*l = strings.FieldsFunc(one, func(r rune) bool { return r == ',' || r == ' ' })
	}
	return nil
}

type pluginAsk struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// parsePluginRequests reads the PLUGINS section: catalog plugins that are not
// installed and enabled, once each, at most maxPluginRequests. Names outside
// the catalog are dropped (decsr never installs from an unknown source) and
// reported in warnings.
func parsePluginRequests(output string, opts SplitOptions) (reqs []PluginRequest, warnings []string) {
	raw, ok := section(output, markerPlugins)
	if !ok {
		return nil, nil
	}
	var asks []pluginAsk
	if unmarshalArray(raw, &asks) != nil {
		return nil, nil
	}
	cat := map[string]bool{}
	for _, e := range opts.Catalog {
		cat[e.Name] = true
	}
	seen := map[string]bool{}
	for _, a := range asks {
		name := strings.TrimSpace(a.Name)
		switch {
		case seen[name]:
			continue
		case !cat[name]:
			warnings = append(warnings, fmt.Sprintf("plugin %q is not in the catalog and was ignored", cutRunes(Sanitize(name), 40)))
			continue
		case opts.Plugins.State(name) == plugins.StateEnabled:
			continue
		case len(reqs) >= maxPluginRequests:
			warnings = append(warnings, fmt.Sprintf("more than %d plugins requested; %q ignored", maxPluginRequests, name))
			continue
		}
		seen[name] = true
		reqs = append(reqs, PluginRequest{Name: name, Reason: cutRunes(Sanitize(a.Reason), maxReasonRunes), Status: PluginPending})
	}
	return reqs, warnings
}

// allowedSkills is the set of skill ids a node may use: the skills of the
// enabled plugins plus those of the catalog plugins the lead asked for.
func allowedSkills(opts SplitOptions, reqs []PluginRequest) map[string]bool {
	ok := map[string]bool{}
	for _, p := range opts.Plugins.Plugins {
		if !p.Enabled {
			continue
		}
		for _, s := range p.Skills {
			ok[s.ID] = true
		}
	}
	for _, r := range reqs {
		for _, e := range opts.Catalog {
			if e.Name != r.Name {
				continue
			}
			for _, s := range e.Skills {
				ok[e.Name+":"+s] = true
			}
		}
	}
	return ok
}

// filterSkills keeps the ids of one node that are allowed, once each. The
// dropped ones come back for the warning.
func filterSkills(ids []string, allowed map[string]bool) (kept, dropped []string) {
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if config.ValidQualifiedName(id) && allowed[id] {
			kept = append(kept, id)
		} else {
			dropped = append(dropped, cutRunes(Sanitize(id), 60))
		}
	}
	sort.Strings(dropped)
	return kept, dropped
}
