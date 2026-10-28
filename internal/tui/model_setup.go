package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/config"
)

// applyProjectSetup persists per-agent overrides from a project-setup completion
// and returns the reloaded merged config. The bool is false when save/reload fails.
func applyProjectSetup(root string, msg setupDoneMsg) (config.Config, bool) {
	cfg, err := applyProjectSetupWithError(root, msg)
	return cfg, err == nil
}

// applyProjectSetupWithError is applyProjectSetup with a real error result so
// the setup screen can report incremental save failures.
func applyProjectSetupWithError(root string, msg setupDoneMsg) (config.Config, error) {
	projectAgents := make(map[string]config.AgentConfig)
	for role, ov := range msg.agentOverrides {
		ac := config.AgentConfig{}
		if ov.runner != "" {
			ac.Runner = ov.runner
		}
		if ov.model != "" {
			ac.Model = ov.model
		}
		if ov.effort != "" {
			ac.Effort = ov.effort
		}
		projectAgents[role] = ac
	}

	projectCfg := config.LoadProject(root)
	if len(projectAgents) > 0 {
		projectCfg.Agents = projectAgents
	} else {
		projectCfg.Agents = nil
	}
	if msg.progLanguage != "" {
		projectCfg.Project.Language = msg.progLanguage
	}
	projectCfg.Project.Name = msg.projectName
	if err := config.Save(root, projectCfg); err != nil {
		return config.Config{}, err
	}

	reloaded, err := config.Load(root)
	if err != nil {
		return config.Config{}, err
	}
	return reloaded, nil
}

// saveProjectSetup persists the current project values incrementally.
func saveProjectSetup(root string, msg setupDoneMsg) error {
	_, err := applyProjectSetupWithError(root, msg)
	return err
}

// cloneAgentConfigs makes a shallow copy of the agent map so incremental global
// saves can update provider/model/effort without mutating the caller's config.
func cloneAgentConfigs(src map[string]config.AgentConfig) map[string]config.AgentConfig {
	if src == nil {
		return nil
	}
	dst := make(map[string]config.AgentConfig, len(src))
	for role, ac := range src {
		dst[role] = ac
	}
	return dst
}

// saveGlobalSetup applies the selected provider/model/effort to every agent and
// writes ~/.decsr/config.yaml.
func saveGlobalSetup(baseAgents map[string]config.AgentConfig, msg setupDoneMsg) error {
	globalAgents := make(map[string]config.AgentConfig, len(baseAgents))
	for role, ac := range baseAgents {
		ac.Runner = msg.provider
		if msg.model != "" {
			ac.Model = msg.model
		}
		ac.Effort = msg.effort
		globalAgents[role] = ac
	}
	return config.SaveGlobal(config.Config{Agents: globalAgents})
}

// modelSetupModel is a standalone Bubble Tea model that shows only the per-agent
// project setup screen. It is used by the "Pause & change model" flow so the
// user can swap an agent's model mid-task without going through the home menu.
type modelSetupModel struct {
	setup  SetupModel
	root   string
	cfg    config.Config
	width  int
	height int
}

func newModelSetupModel(root string, cfg config.Config) modelSetupModel {
	currentRunner, currentModel, currentEffort := detectDefaultRunnerModel(cfg.Agents)
	projectCfg := config.LoadProject(root)
	projectOverrides := make(map[string]config.AgentConfig)
	if projectCfg.Agents != nil {
		projectOverrides = projectCfg.Agents
	}
	setup := NewSetupModelWithOverrides(currentRunner, currentModel, currentEffort,
		cfg.Project.Language, cfg.Project.Name, projectOverrides)
	setup.root = root
	return modelSetupModel{setup: setup, root: root, cfg: cfg, width: 80, height: 24}
}

func (m modelSetupModel) Init() tea.Cmd { return m.setup.Init() }

func (m modelSetupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if wm, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = wm.Width, wm.Height
		m.setup.width, m.setup.height = wm.Width, wm.Height
		m.setup.syncViewport()
	}
	switch msg := msg.(type) {
	case setupDoneMsg:
		if reloaded, ok := applyProjectSetup(m.root, msg); ok {
			m.cfg = reloaded
		}
		return m, tea.Quit
	case setupCancelledMsg:
		return m, tea.Quit
	case setupClosedMsg:
		if reloaded, err := config.Load(m.root); err == nil {
			m.cfg = reloaded
		}
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.setup, cmd = m.setup.Update(msg)
	return m, cmd
}

func (m modelSetupModel) View() string {
	return paintBackground(m.setup.View(), m.width, m.height)
}

// RunModelSetup shows only the per-agent project setup screen and returns the
// (possibly updated) config. Used by the pause-to-change-model flow.
func RunModelSetup(root string, cfg config.Config) (config.Config, error) {
	m := newModelSetupModel(root, cfg)
	m.setup.syncViewport()
	prog := tea.NewProgram(m, tea.WithAltScreen())
	result, err := prog.Run()
	if err != nil {
		return cfg, err
	}
	if final, ok := result.(modelSetupModel); ok {
		return final.cfg, nil
	}
	return cfg, nil
}
