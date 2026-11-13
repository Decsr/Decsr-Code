package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/agent"
	"github.com/Decsr/Decsr-Code/internal/artifacts"
	"github.com/Decsr/Decsr-Code/internal/bus"
	"github.com/Decsr/Decsr-Code/internal/config"
	"github.com/Decsr/Decsr-Code/internal/decsr"
	"github.com/Decsr/Decsr-Code/internal/executil"
	"github.com/Decsr/Decsr-Code/internal/logging"
	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/safefile"
	"github.com/Decsr/Decsr-Code/internal/skills"
	"github.com/Decsr/Decsr-Code/internal/tui"
)

// version is set at build time via -ldflags "-X main.version=<ver>".
var version = "dev"

func main() {
	logging.Setup()
	tui.Version = version
	if len(os.Args) < 2 {
		// No sub-command: open the TUI picker and let the user choose requirements.
		runCmd(nil)
		return
	}

	switch os.Args[1] {
	case "run":
		runCmd(os.Args[2:])
	case "task":
		taskCmd(os.Args[2:])
	case "agent":
		agentCmd(os.Args[2:])
	case "resume":
		opts, _ := subcommandWorkspaceOptions("resume")
		runCmdWith(os.Args[2:], opts)
	case "report":
		reportCmd(os.Args[2:])
	case "monitor":
		_, _ = fmt.Fprintln(os.Stderr, "monitor not implemented yet")
		os.Exit(2)
	case "approve":
		approveCmd(os.Args[2:])
	case "clean":
		cleanCmd(os.Args[2:])
	case "memory":
		memoryCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`decsr <command>

	Commands:
	  run --requirements requirements.md  Run a full orchestration workflow (legacy greenfield)
	  task --description "..."            Run a unified task (feature, bugfix, refactor, or greenfield)
	  task --from-file task.md            Read task description from a file
	  (both task and run accept --no-context-pack to skip the context pack phase)
	  resume                              Open the Workspace with this project's saved sessions listed
	  report                              Summarize last run artifacts
	  monitor                             Open the control TUI against a running bus
	  approve                             Create approval marker
	  clean                               Remove .decsr workspace
	  memory <subcommand>                 Inspect/manage project memory (show|search|reindex|add|stats)`)

}

// workspaceStep is what the entry loop does after the Workspace closes.
type workspaceStep int

const (
	stepQuit   workspaceStep = iota // leave decsr
	stepMenu                        // open the classic Home menu
	stepSwitch                      // reopen the Workspace in another project
)

// workspaceNext decides what follows a closed Workspace. A project switch
// beats the menu; an empty result quits.
func workspaceNext(r tui.WorkspaceResult) (workspaceStep, string) {
	switch {
	case r.SwitchProject != "":
		return stepSwitch, r.SwitchProject
	case r.OpenMenu:
		return stepMenu, ""
	}
	return stepQuit, ""
}

// subcommandWorkspaceOptions reports whether sub is a sub-command that opens
// the Workspace, and how.
func subcommandWorkspaceOptions(sub string) (tui.WorkspaceOptions, bool) {
	if sub == "resume" {
		return tui.WorkspaceOptions{Resume: true}, true
	}
	return tui.WorkspaceOptions{}, false
}

func runCmd(args []string) { runCmdWith(args, tui.WorkspaceOptions{}) }

// runCmdWith is runCmd with options for the first Workspace that opens.
func runCmdWith(args []string, wsOpts tui.WorkspaceOptions) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	reqPath := fs.String("requirements", "", "path to requirements.md (omit to open picker)")
	branch := fs.String("branch", "", "git branch to create and checkout")
	modulePath := fs.String("module", "", "Go module path (e.g. github.com/user/project)")
	dryRun := fs.Bool("dry-run", false, "print intent and exit")
	ui := fs.String("ui", "auto", "ui mode: auto, tui, or plain")
	noContextPack := fs.Bool("no-context-pack", false, "skip the repository context pack phase")
	_ = fs.Parse(args)

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	cfg, err := config.Load(root)
	if err != nil {
		fatal(fmt.Errorf("load config: %w", err))
	}
	if *noContextPack {
		cfg.Project.Context.Pack.Disable = true
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(err)
	}

	if err := logging.SetupFile(ws.Dir, logging.LogFileName); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warn: could not open log file: %v\n", err)
	}
	defer logging.Close()

	// Migrate legacy module_path file into project config.
	if cfg.Project.ModulePath == "" {
		if data, err := ws.ReadFile(artifacts.ModulePathFile); err == nil {
			cfg.Project.ModulePath = strings.TrimSpace(string(data))
			_ = os.Remove(ws.Path(artifacts.ModulePathFile))
		}
	}

	// Override with --module flag if provided.
	if *modulePath != "" {
		cfg.Project.ModulePath = *modulePath
	}

	// Auto-detect from existing go.mod if still empty.
	if cfg.Project.ModulePath == "" {
		cfg.Project.ModulePath = detectGoModulePath(root)
	}

	// Persist module path to project config for future runs.
	if cfg.Project.ModulePath != "" {
		projectCfg := config.LoadProject(root)
		if projectCfg.Project.ModulePath != cfg.Project.ModulePath {
			projectCfg.Project.ModulePath = cfg.Project.ModulePath
			_ = config.Save(root, projectCfg)
		}
	}

	// If module path is still missing for a Go project, prompt the user.
	// Only when --requirements is provided (TUI startup handles this internally).
	if *reqPath != "" && tui.NeedsModulePath(root, cfg) && !*dryRun && *ui != "plain" {
		chosen, err := tui.RunModulePathPrompt(root, ws.Path(""))
		if err != nil {
			fatal(fmt.Errorf("module path prompt: %w", err))
		}
		if chosen == "" {
			return // user cancelled
		}
		cfg.Project.ModulePath = chosen
	}

	// No requirements given — open the Workspace (unless plain/dry-run mode).
	// "/menu" in the Workspace opens the classic home/picker; choosing the
	// WORKSPACE item there returns to the Workspace, so loop until the user
	// picks something else or quits.
	if *reqPath == "" && !*dryRun && *ui != "plain" {
		var result tui.StartupResult
		_ = tui.SaveRecentProject(root) // so /project can come back to this project
		for {
			wsResult, err := tui.RunWorkspace(root, cfg, wsOpts)
			wsOpts = tui.WorkspaceOptions{} // only the first Workspace opens on the session list
			if err != nil {
				fatal(fmt.Errorf("workspace: %w", err))
			}
			cfg = wsResult.Cfg
			step, target := workspaceNext(wsResult)
			if step == stepQuit {
				return // user quit the Workspace
			}
			if step == stepSwitch {
				if chErr := os.Chdir(target); chErr != nil {
					// Stay in the current project and reopen the Workspace there.
					_, _ = fmt.Fprintf(os.Stderr, "cannot switch to %s: %v\n", target, chErr)
					time.Sleep(2 * time.Second)
					continue
				}
				_ = tui.SaveRecentProject(target)
				root = target
				if reloadedCfg, loadErr := config.Load(root); loadErr == nil {
					cfg = reloadedCfg
				}
				if *noContextPack {
					cfg.Project.Context.Pack.Disable = true
				}
				ws, err = artifacts.EnsureWorkspace(root)
				if err != nil {
					fatal(err)
				}
				if logErr := logging.SetupFile(ws.Dir, logging.LogFileName); logErr != nil {
					_, _ = fmt.Fprintf(os.Stderr, "warn: could not open log file: %v\n", logErr)
				}
				continue
			}

			result, err = tui.RunStartup(root, ws.Path(artifacts.RequirementsFile), cfg)
			if err != nil {
				fatal(fmt.Errorf("picker: %w", err))
			}
			cfg = result.Cfg
			if *noContextPack {
				cfg.Project.Context.Pack.Disable = true
			}

			// Project may have changed via project picker (os.Chdir).
			// Refresh root, config, and workspace to match the selected project.
			newRoot, err := os.Getwd()
			if err == nil && newRoot != root {
				root = newRoot
				if reloadedCfg, loadErr := config.Load(root); loadErr == nil {
					cfg = reloadedCfg
					if *noContextPack {
						cfg.Project.Context.Pack.Disable = true
					}
				}
				ws, err = artifacts.EnsureWorkspace(root)
				if err != nil {
					fatal(err)
				}
			}

			if !returnsToWorkspace(result) {
				break
			}
		}

		if result.ReqPath != "" {
			*reqPath = result.ReqPath
		} else if result.Resume {
			// User chose to resume the interrupted task.
			runTaskFlow(root, "", *branch, *ui, cfg, ws, true)
			return
		} else if result.ReviewProject {
			// User chose "Resume Project": seed the PM with a digest of the
			// existing .decsr artifacts so it plans the remaining work.
			seed := decsr.BuildProjectReviewSeed(root)
			runTaskFlow(root, seed, *branch, *ui, cfg, ws, false)
			return
		} else if result.ChatMode {
			// User picked "New Task" — route to the brownfield-aware TaskRunner
			// (PM negotiation gathers the description from the user).
			runTaskFlow(root, "", *branch, *ui, cfg, ws, false)
			return
		} else {
			return // user quit
		}
	}

	if *reqPath == "" && *ui == "plain" {
		_, _ = fmt.Fprintln(os.Stderr, "--requirements is required in plain mode")
		os.Exit(2)
	}

	if *branch != "" {
		if err := checkoutBranch(root, *branch); err != nil {
			fatal(err)
		}
	}

	if *dryRun {
		fmt.Println("dry-run: workspace prepared, no agents executed")
		return
	}

	// Read requirements file and route through TaskRunner.
	reqs, err := os.ReadFile(*reqPath)
	if err != nil {
		fatal(fmt.Errorf("read requirements: %w", err))
	}
	runTaskFlow(root, string(reqs), *branch, *ui, cfg, ws, false)
}

// returnsToWorkspace reports whether the classic menu result asks to go back
// to the Workspace. A concrete pipeline choice always wins over it.
func returnsToWorkspace(r tui.StartupResult) bool {
	return r.Workspace && r.ReqPath == "" && !r.Resume && !r.ReviewProject && !r.ChatMode
}

// runTaskFlow launches the task runner with the given description. When resume
// is true, taskInput is ignored and the runner resumes the interrupted task.
// Extracted so both the startup "New Task" flow and the `task` subcommand can reuse it.
func runTaskFlow(root, taskInput, branch, uiMode string, cfg config.Config, ws artifacts.Workspace, resume bool) {
	if branch != "" {
		if err := checkoutBranch(root, branch); err != nil {
			fatal(err)
		}
	}

	resolvedUI := resolveUIMode(uiMode)

	ctx := context.Background()

	if resolvedUI == "plain" {
		b := bus.New()
		if err := b.SetLogPath(ws.Dir, "runlog.jsonl"); err != nil {
			slog.Warn("could not open bus log", slog.String("error", err.Error()))
		}
		defer b.Close()
		agents := buildAgents(b, cfg, ws, root)
		taskRunner := decsr.NewTaskRunner(b, agents, cfg, ws, root)
		taskRunner.SetInteractive(false)
		events := b.Subscribe()
		go plainLogger(events)
		var runErr error
		if resume {
			runErr = taskRunner.Resume(ctx)
		} else {
			runErr = taskRunner.Run(ctx, taskInput)
		}
		if runErr != nil {
			fatal(runErr)
		}
		return
	}

	b := bus.New()
	if err := b.SetLogPath(ws.Dir, "runlog.jsonl"); err != nil {
		slog.Warn("could not open bus log", slog.String("error", err.Error()))
	}
	agents := buildAgents(b, cfg, ws, root)
	events := b.Subscribe()
	wsPath := ws.Path("")
	chatLLM := chatRunner(cfg)
	tuiModel := tui.New(events, root, wsPath, chatLLM, cfg).WithTaskInput(taskInput)
	p := tea.NewProgram(tuiModel, tea.WithAltScreen())

	pipelineCtx, pipelineCancel := context.WithCancel(ctx)
	defer pipelineCancel()

	pipelineDone := make(chan struct{})
	go func() {
		defer close(pipelineDone)
		taskRunner := decsr.NewTaskRunner(b, agents, cfg, ws, root)
		p.Send(tui.TaskRunnerReadyMsg{Runner: taskRunner, Cancel: pipelineCancel})
		var err error
		if resume {
			err = taskRunner.Resume(pipelineCtx)
		} else {
			err = taskRunner.Run(pipelineCtx, taskInput)
		}
		if pipelineCtx.Err() != nil {
			err = fmt.Errorf("task cancelled by user")
		}
		p.Send(tui.PipelineDoneMsg{Err: err})
	}()

	result, err := p.Run()
	// Any exit path (quit confirm, Ctrl+C, return to menu) must stop the
	// pipeline so runner subprocesses are killed rather than orphaned.
	stopPipeline(pipelineCancel, pipelineDone, pipelineShutdownTimeout)
	b.Close()
	if err != nil {
		fatal(err)
	}

	final, ok := result.(tui.Model)
	if !ok {
		return
	}

	// The pipeline is already stopped above, so a follow-up flow never runs
	// alongside it on .decsr artifacts and bd state.
	// Pause-to-change-model: show the per-agent setup screen, then resume the
	// interrupted task with the updated config.
	if final.PauseForModelChange() {
		newCfg, setupErr := tui.RunModelSetup(root, cfg)
		if setupErr != nil {
			fatal(fmt.Errorf("model setup: %w", setupErr))
		}
		runTaskFlow(root, "", "", uiMode, newCfg, ws, true)
		return
	}

	if !final.ReturnToMenu() {
		return
	}

	startupResult, startupErr := tui.RunStartup(root, ws.Path(artifacts.RequirementsFile), cfg)
	if startupErr != nil {
		fatal(fmt.Errorf("picker: %w", startupErr))
	}
	cfg = startupResult.Cfg

	newRoot, rootErr := os.Getwd()
	if rootErr == nil && newRoot != root {
		root = newRoot
		if reloadedCfg, loadErr := config.Load(root); loadErr == nil {
			cfg = reloadedCfg
		}
		ws, err = artifacts.EnsureWorkspace(root)
		if err != nil {
			fatal(err)
		}
	}

	if returnsToWorkspace(startupResult) {
		// Re-enter the default entry point, which opens the Workspace in the
		// current (possibly switched) project.
		args := []string{"--ui", uiMode}
		if cfg.Project.Context.Pack.Disable {
			args = append(args, "--no-context-pack")
		}
		runCmd(args)
		return
	}
	if startupResult.ReqPath != "" {
		args := []string{"--requirements", startupResult.ReqPath, "--ui", uiMode}
		if cfg.Project.Context.Pack.Disable {
			args = append(args, "--no-context-pack")
		}
		runCmd(args)
		return
	}
	if startupResult.Resume {
		runTaskFlow(root, "", "", uiMode, cfg, ws, true)
		return
	}
	if startupResult.ReviewProject {
		seed := decsr.BuildProjectReviewSeed(root)
		runTaskFlow(root, seed, "", uiMode, cfg, ws, false)
		return
	}
	if startupResult.ChatMode {
		// Route straight into the PM chat flow. Re-invoking runCmd here would
		// show the home menu a second time (it opens the startup picker when no
		// requirements are given), forcing a second "New Task" click.
		runTaskFlow(root, "", "", uiMode, cfg, ws, false)
		return
	}
}

func taskCmd(args []string) {
	fs := flag.NewFlagSet("task", flag.ExitOnError)
	description := fs.String("description", "", "task description (omit for interactive)")
	fromFile := fs.String("from-file", "", "read task description from a file")
	branch := fs.String("branch", "", "git branch to create and checkout")
	ui := fs.String("ui", "auto", "ui mode: auto, tui, or plain")
	noContextPack := fs.Bool("no-context-pack", false, "skip the repository context pack phase")
	_ = fs.Parse(args)

	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}

	cfg, err := config.Load(root)
	if err != nil {
		fatal(fmt.Errorf("load config: %w", err))
	}
	if *noContextPack {
		cfg.Project.Context.Pack.Disable = true
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(err)
	}

	if err := logging.SetupFile(ws.Dir, logging.LogFileName); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warn: could not open log file: %v\n", err)
	}
	defer logging.Close()

	// Auto-detect Go module path.
	if cfg.Project.ModulePath == "" {
		cfg.Project.ModulePath = detectGoModulePath(root)
	}

	taskInput := *description
	if *fromFile != "" {
		data, readErr := safefile.ReadFile(filepath.Dir(*fromFile), filepath.Base(*fromFile))
		if readErr != nil {
			fatal(fmt.Errorf("read task file: %w", readErr))
		}
		taskInput = string(data)
	}

	if taskInput == "" {
		_, _ = fmt.Fprintln(os.Stderr, "task requires --description or --from-file")
		os.Exit(2)
	}

	runTaskFlow(root, taskInput, *branch, *ui, cfg, ws, false)
}

func resolveUIMode(requested string) string {
	switch requested {
	case "", "auto":
		return "tui"
	case "plain", "tui":
		return requested
	case "tmux", "cmux", "cmux-internal":
		slog.Warn("ui mode no longer supported, using tui", slog.String("requested", requested))
		return "tui"
	default:
		slog.Warn("unknown ui mode, falling back to tui", slog.String("requested", requested))
		return "tui"
	}
}

func buildAgents(b *bus.Bus, cfg config.Config, ws artifacts.Workspace, root string) map[bus.AgentRole]agent.Agent {
	// Derive per-agent context limits from the global RAM/context setting before
	// constructing runners so both the runners and the review agents pick up the
	// computed MaxContextTokens.
	runner.ApplyModelMemory(&cfg)

	skillLoader := skills.New("")
	var allSkills []string
	for _, ac := range cfg.Agents {
		allSkills = append(allSkills, ac.Skills...)
	}
	if len(allSkills) > 0 {
		go func() {
			if err := skillLoader.Prefetch(allSkills); err != nil {
				slog.Warn("skill prefetch failed", slog.String("error", err.Error()))
			}
		}()
	}

	makeRunner := func(role string) runner.LLMRunner {
		ac := cfg.Agents[role]
		r, err := runner.New(ac, skillLoader, cfg.PromptLanguage)
		if err != nil {
			slog.Warn("runner creation failed, falling back to opencode",
				slog.String("role", role),
				slog.String("error", err.Error()),
			)
			return runner.OpenCodeRunner{}
		}
		return r
	}

	agents := make(map[bus.AgentRole]agent.Agent)

	pmCfg := cfg.Agents["pm"]
	agents[bus.RolePM] = agent.NewPMAgent(b, makeRunner("pm"), ws,
		pmCfg.Skills, pmCfg.Model)

	coderCfg := cfg.Agents["coder"]
	coderAgent := agent.NewCoderAgent(b, makeRunner("coder"), ws, root, cfg,
		coderCfg.Skills, coderCfg.Model)
	if fixerCfg, ok := cfg.Agents["coder_fixer"]; ok && fixerCfg.Runner != "" {
		coderAgent.SetFixerRunner(makeRunner("coder_fixer"), fixerCfg.Model)
	}
	agents[bus.RoleCoder] = coderAgent

	qaCfg := cfg.Agents["qa"]
	qaAgent := agent.NewQAAgent(b, makeRunner("qa"), ws, root, cfg,
		qaCfg.Skills, qaCfg.Model)
	qaAgent.SetMaxContextTokens(qaCfg.MaxContextTokens)
	agents[bus.RoleQA] = qaAgent

	uxCfg := cfg.Agents["ux_reviewer"]
	uxAgent := agent.NewUXReviewerAgent(b, makeRunner("ux_reviewer"), ws,
		uxCfg.Skills, uxCfg.Model)
	uxAgent.SetMaxContextTokens(uxCfg.MaxContextTokens)
	agents[bus.RoleUXReviewer] = uxAgent

	secCfg := cfg.Agents["security"]
	secAgent := agent.NewSecurityAgent(b, makeRunner("security"), ws,
		secCfg.Skills, secCfg.Model)
	secAgent.SetMaxContextTokens(secCfg.MaxContextTokens)
	agents[bus.RoleSecurity] = secAgent

	return agents
}

func plainLogger(events <-chan bus.Message) {
	for msg := range events {
		switch msg.Type {
		case bus.MsgHumanGate:
			fmt.Printf("\n[GATE] %v\n\n", msg.Payload)
		case bus.MsgConversation:
			if conv, ok := msg.Payload.(bus.ConversationPayload); ok && strings.TrimSpace(conv.Content) != "" {
				fmt.Printf("\n[PM] %s\n\n", conv.Content)
			}
		case bus.MsgEvent:
			if tp, ok := msg.Payload.(bus.TokenPayload); ok {
				if !tp.Done {
					fmt.Print(tp.Text)
				}
			} else {
				fmt.Printf("[%s] %v\n", msg.From, msg.Payload)
			}
		}
	}
}

func reportCmd(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root (default: cwd)")
	_ = fs.Parse(args)

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fatal(err)
		}
		root = cwd
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(err)
	}

	printReport(os.Stdout, ws)
}

// reportFiles lists the run artifacts shown by "decsr report", in order.
var reportFiles = []string{
	artifacts.SummaryFile,
	artifacts.TaskPlanFile,
	artifacts.ResearchReportFile,
	artifacts.ImplementationPlanFile,
	artifacts.TestReportFile,
	artifacts.ReviewFile,
	artifacts.UXReviewFile,
	artifacts.SecurityReviewFile,
	artifacts.NiceToHaveFile,
}

// printReport writes every existing report artifact to w, or a hint when the
// workspace has none.
func printReport(w io.Writer, ws artifacts.Workspace) {
	found := false
	for _, name := range reportFiles {
		if printIfExists(w, ws, name) {
			found = true
		}
	}
	if !found {
		_, _ = fmt.Fprintf(w, "no reports yet in %s — run `decsr task` first\n", ws.Dir)
	}
}

func approveCmd(args []string) {
	fs := flag.NewFlagSet("approve", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root (default: cwd)")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: decsr approve <vision|architecture|plan|prompts|all>")
		os.Exit(2)
	}

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fatal(err)
		}
		root = cwd
	}

	ws, err := artifacts.EnsureWorkspace(root)
	if err != nil {
		fatal(err)
	}

	target := strings.ToLower(fs.Arg(0))
	markers := markerList(target)
	if markers == nil {
		_, _ = fmt.Fprintf(os.Stderr, "unknown approval target: %s\n", target)
		os.Exit(2)
	}
	for _, m := range markers {
		if err := writeApproval(ws, m); err != nil {
			fatal(err)
		}
	}
}

func markerList(target string) []string {
	switch target {
	case "vision":
		return []string{artifacts.VisionApprovedFile}
	case "architecture":
		return []string{artifacts.ArchitectureApprovedFile}
	case "plan":
		return []string{artifacts.PlanApprovedFile}
	case "prompts":
		return []string{artifacts.PromptsApprovedFile}
	case "all":
		return []string{
			artifacts.VisionApprovedFile,
			artifacts.ArchitectureApprovedFile,
			artifacts.PlanApprovedFile,
			artifacts.PromptsApprovedFile,
		}
	}
	return nil
}

func cleanCmd(args []string) {
	fs := flag.NewFlagSet("clean", flag.ExitOnError)
	rootFlag := fs.String("root", "", "repo root (default: cwd)")
	_ = fs.Parse(args)

	root := *rootFlag
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fatal(err)
		}
		root = cwd
	}

	dir := filepath.Join(root, artifacts.DirName)
	kept, err := cleanDecsrDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("nothing to clean")
			return
		}
		fatal(err)
	}
	fmt.Println(cleanSummary(dir, kept))
}

// cleanDecsrDir removes generated artifacts from dir and returns the sorted
// names of the entries it kept: requirements, project config, and project
// memory (memory/ and the memory.db index with its sqlite sidecar files).
func cleanDecsrDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var kept []string
	for _, entry := range entries {
		name := entry.Name()
		if name == artifacts.RequirementsFile || name == artifacts.ProjectConfigFile ||
			name == artifacts.MemoryDirName || strings.HasPrefix(name, artifacts.MemoryDBFile) {
			if entry.IsDir() {
				name += "/"
			}
			kept = append(kept, name)
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return nil, err
		}
	}
	sort.Strings(kept)
	return kept, nil
}

func cleanSummary(dir string, kept []string) string {
	return fmt.Sprintf("cleaned %s (kept: %s)", dir, strings.Join(kept, ", "))
}

func printIfExists(w io.Writer, ws artifacts.Workspace, name string) bool {
	data, err := ws.ReadFile(name)
	if err != nil {
		return false
	}
	// Pretty-print JSON files.
	if strings.HasSuffix(name, ".json") {
		var v any
		if json.Unmarshal(data, &v) == nil {
			pretty, _ := json.MarshalIndent(v, "", "  ")
			_, _ = fmt.Fprintf(w, "== %s ==\n%s\n\n", name, pretty)
			return true
		}
	}
	_, _ = fmt.Fprintf(w, "== %s ==\n%s\n\n", name, strings.TrimSpace(string(data)))
	return true
}

func writeApproval(ws artifacts.Workspace, marker string) error {
	if err := ws.WriteFile(marker, []byte("approved\n")); err != nil {
		return err
	}
	fmt.Println("created", ws.Path(marker))
	return nil
}

func checkoutBranch(root, branch string) error {
	cmd := executil.Command("git", "checkout", "-b", branch)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err == nil {
		return nil
	}
	cmd = executil.Command("git", "checkout", branch)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// detectGoModulePath reads the module path from an existing go.mod file.
func detectGoModulePath(root string) string {
	data, err := safefile.ReadFile(root, "go.mod")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module"))
		}
	}
	return ""
}

// pipelineShutdownTimeout bounds how long a follow-up flow waits for the
// cancelled pipeline goroutine to exit.
const pipelineShutdownTimeout = 15 * time.Second

// waitForPipeline blocks until done is closed or timeout elapses (logging a
// warning in the latter case). Returns true when the pipeline exited.
func waitForPipeline(done <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		slog.Warn("previous pipeline did not stop in time; continuing", slog.Duration("timeout", timeout))
		return false
	}
}

// stopPipeline cancels the pipeline context and waits (bounded) for the
// pipeline goroutine to exit, so subprocesses started with
// exec.CommandContext are killed before the process returns. Returns true
// when the pipeline exited within the timeout.
func stopPipeline(cancel context.CancelFunc, done <-chan struct{}, timeout time.Duration) bool {
	cancel()
	return waitForPipeline(done, timeout)
}

// chatRunner builds the PM chat overlay runner from the configured PM agent
// using the same factory as the pipeline, so chat honours the user's runner,
// model, effort and context limit. Falls back to opencode like buildAgents.
func chatRunner(cfg config.Config) runner.LLMRunner {
	runner.ApplyModelMemory(&cfg)
	r, err := runner.New(cfg.Agents["pm"], nil, cfg.PromptLanguage)
	if err != nil {
		slog.Warn("chat runner creation failed, falling back to opencode", slog.String("error", err.Error()))
		return runner.OpenCodeRunner{}
	}
	return r
}
