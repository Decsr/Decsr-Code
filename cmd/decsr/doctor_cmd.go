package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/Decsr/Decsr-Code/internal/automation"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/credentials"
	"github.com/Decsr/Decsr-Code/internal/doctor"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/workspace"
)

const doctorCmdTimeout = 3 * time.Second

// doctorCmd checks the environment and exits 1 when a required check fails.
func doctorCmd(_ []string) {
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	rs := doctor.Run(buildDoctorDeps(root))
	doctor.Print(os.Stdout, rs)
	if doctor.Failed(rs) {
		os.Exit(1)
	}
}

func buildDoctorDeps(root string) doctor.Deps {
	home, _ := os.UserHomeDir()
	cfg, cfgErr := config.Load(root)

	names := make([]string, 0, len(cfg.Agents))
	for n := range cfg.Agents {
		names = append(names, n)
	}
	sort.Strings(names)
	agents := make([]doctor.Agent, 0, len(names))
	for _, n := range names {
		a := cfg.Agents[n]
		agents = append(agents, doctor.Agent{Name: n, Runner: a.Runner, BaseURL: a.BaseURL, APIKeyEnv: a.APIKeyEnv, Provider: a.Provider})
	}
	// Mode applies to workspace roles; a custom role inherits pm's runner.
	for _, r := range workspace.Roles(cfg) {
		if err := r.Agent.ModeError(); err != nil && !r.Plugin {
			for i := range agents {
				if agents[i].Name == r.Name {
					agents[i].ModeErr = err.Error()
				}
			}
		}
	}

	layers := []doctor.Layer{}
	if home != "" {
		layers = append(layers, doctorLayer("global", filepath.Join(home, config.GlobalDir, config.GlobalFilename)))
	}
	layers = append(layers,
		doctorLayer("project", filepath.Join(root, config.GlobalDir, config.ProjectFilename)),
		doctorLayer("legacy project", filepath.Join(root, config.Filename)),
	)

	return doctor.Deps{
		LookPath:  exec.LookPath,
		Getenv:    os.Getenv,
		Run:       runDoctorCmd,
		Reachable: runner.ProviderReachable,
		KeySource: runner.KeySource,
		CredsErr:  credentials.Default().CheckPerms,
		PresetKeyEnv: func(name string) string {
			p, _ := runner.PresetByName(name)
			return p.KeyEnv
		},
		Writable:    dirWritable,
		Root:        root,
		Home:        home,
		Agents:      agents,
		Layers:      layers,
		ConfigErr:   cfgErr,
		Theme:       cfg.UI.Theme,
		Automations: automationDoctor(root, automation.ExecCrontab{}),
		Notify: doctor.NotifyInfo{
			DisableCMux:    cfg.Notifications.DisableCMux,
			DisableBell:    cfg.Notifications.DisableBell,
			DisableDesktop: cfg.Notifications.DisableDesktop,
			Webhook:        cfg.Notifications.Webhook,
			WebhookFormat:  cfg.Notifications.WebhookFormat,
		},
	}
}

func doctorLayer(name, path string) doctor.Layer {
	_, err := os.Stat(path)
	return doctor.Layer{Name: name, Path: path, Found: err == nil}
}

func runDoctorCmd(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), doctorCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput() // #nosec G204 -- fixed tool names from the doctor checks
	return string(out), err
}

// dirWritable reports whether files can be created in dir. A missing dir is
// judged by its nearest existing parent and is not created.
func dirWritable(dir string) error {
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return fmt.Errorf("%s is not a directory", dir)
			}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return err
		}
		dir = parent
	}
	f, err := os.CreateTemp(dir, ".decsr-doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}
