package config

import (
	"gopkg.in/yaml.v3"

	"github.com/Decsr/Decsr-Code/internal/safefile"
)

// LeadRole is the role that splits tasks in the workspace.
const LeadRole = "pm"

// UseProviderGlobal writes runner, model and (for a saved custom provider)
// provider into ~/.decsr/config.yaml for the lead role only, or for every role.
// Only names are written; an API key never goes into a config file.
func UseProviderGlobal(runnerName, provider, model string, allRoles bool) error {
	roles := []string{LeadRole}
	if allRoles {
		seen := map[string]bool{}
		roles = nil
		add := func(r string) {
			if !seen[r] {
				seen[r] = true
				roles = append(roles, r)
			}
		}
		for _, r := range BuiltinRoles {
			add(r)
		}
		add("pr")
		if d := globalConfigDir(); d != "" {
			if c, err := loadFile(d, GlobalFilename); err == nil {
				for r := range c.Agents {
					add(r)
				}
			}
		}
	}
	agents := make(map[string]AgentConfig, len(roles))
	for _, r := range roles {
		agents[r] = AgentConfig{Runner: runnerName, Provider: provider, Model: model}
	}
	return SaveGlobal(Config{Agents: agents})
}

// UseRunnerGlobalFresh makes runnerName and model the global choice of every
// role, replacing what the roles had before: their model, effort and endpoint
// are reset, so nothing left over from another runner stays. Only names are
// written.
func UseRunnerGlobalFresh(runnerName, model string) error {
	if err := UseProviderGlobal(runnerName, "", model, true); err != nil {
		return err
	}
	dir := globalConfigDir()
	if dir == "" {
		return nil
	}
	cfg, err := loadFile(dir, GlobalFilename)
	if err != nil {
		return err
	}
	for role, ac := range cfg.Agents {
		ac.Runner, ac.Model, ac.Effort = runnerName, model, ""
		ac.BaseURL, ac.Provider, ac.APIKeyEnv = "", "", ""
		cfg.Agents[role] = ac
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return safefile.WriteFile(dir, GlobalFilename, data, 0o600)
}
