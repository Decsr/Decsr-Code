package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/Decsr/Decsr-Code/internal/runner"
	"github.com/Decsr/Decsr-Code/internal/ui"
)

// chatLine is one entry in the chat history view.
type chatLine struct {
	role    string // "user" | "assistant"
	content string
}

// ReviseFunc is called when the PM decides to revise an artifact.
type ReviseFunc func(artifact, feedback string) error

// ChatModel is a live chat panel overlay backed by an LLMRunner.
type ChatModel struct {
	llm          runner.LLMRunner
	history      []runner.ConvMessage
	lines        []chatLine
	input        string
	cursor       int // rune index in input
	vp           viewport.Model
	waiting      bool
	ctx          context.Context
	cancelFn     context.CancelFunc
	width        int
	height       int
	systemPrompt string
	reviseFn     ReviseFunc // called when PM decides to revise an artifact
	revising     bool       // true while a revision is in progress
	gen          int        // stream generation; bumped on cancel so stale stream messages are dropped

	root     string           // repo root, used to discover .md files
	attached []chatAttachment // markdown files added to the chat context
	picking  bool             // true while the .md picker overlay is open
	picker   mdPicker
}

// NewChat creates a ChatModel with the given LLMRunner.
// systemContext is prepended to every request as the system prompt.
func NewChat(llm runner.LLMRunner, systemContext string) ChatModel {
	vp := viewport.New(76, 18)
	ctx, cancel := context.WithCancel(context.Background())
	return ChatModel{
		llm:          llm,
		vp:           vp,
		ctx:          ctx,
		cancelFn:     cancel,
		width:        80,
		height:       24,
		systemPrompt: systemContext,
	}
}

// WithReviseFn sets a callback for artifact revision (PM chat mode).
func (m ChatModel) WithReviseFn(fn ReviseFunc) ChatModel {
	m.reviseFn = fn
	return m
}

// WithFileContext enables the .md attach picker, rooted at the repo directory.
func (m ChatModel) WithFileContext(root string) ChatModel {
	m.root = root
	return m
}

func (m ChatModel) Init() tea.Cmd { return nil }

// chatSendCmd sends the current conversation to the LLM and streams tokens.
func chatSendCmd(llm runner.LLMRunner, ctx context.Context, gen int, systemPrompt string, history []runner.ConvMessage) tea.Cmd {
	return func() tea.Msg {
		ch, err := llm.Complete(ctx, runner.CompletionRequest{
			SystemPrompt: systemPrompt,
			Messages:     history,
		})
		if err != nil {
			return ChatTokenMsg{Text: "error: " + err.Error(), Done: true, Gen: gen}
		}
		// Return the channel wrapped so TUI can drain it.
		return chatStreamStartMsg{ch: ch, gen: gen}
	}
}

// chatStreamStartMsg carries the token channel to the Update loop.
type chatStreamStartMsg struct {
	ch  <-chan runner.Token
	gen int
}

// chatDrainCmd reads one token from the channel.
func chatDrainCmd(ch <-chan runner.Token, gen int) tea.Cmd {
	return func() tea.Msg {
		tok, ok := <-ch
		if !ok {
			return ChatTokenMsg{Done: true, Gen: gen}
		}
		if tok.Error != nil {
			return ChatTokenMsg{Text: "error: " + tok.Error.Error(), Done: true, Gen: gen}
		}
		return chatRawToken{text: tok.Text, done: tok.Done, ch: ch, gen: gen}
	}
}

type chatRawToken struct {
	text string
	done bool
	ch   <-chan runner.Token
	gen  int
}

func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	switch msg := msg.(type) {
	case mdPickerDoneMsg:
		m.picking = false
		if !msg.cancel {
			m.attached = loadAttachments(m.root, msg.selected)
		}
		m.refreshViewport()
		return m, nil

	case tea.KeyMsg:
		if m.picking {
			var cmd tea.Cmd
			m.picker, cmd = m.picker.Update(msg)
			return m, cmd
		}
		if m.waiting {
			// Only allow cancel while waiting.
			if msg.String() == "ctrl+c" || msg.String() == "esc" {
				m.cancelFn()
				m.gen++
				m.waiting = false
				// Recreate context for next use.
				m.ctx, m.cancelFn = context.WithCancel(context.Background())
				m.refreshViewport()
			}
			return m, nil
		}

		switch msg.String() {
		case "esc":
			m.cancelFn()
			return m, func() tea.Msg { return ChatClosedMsg{} }
		case "ctrl+f":
			if m.root != "" {
				m.picker = newMDPicker(m.root, m.attached, m.vp.Width, m.vp.Height)
				m.picking = true
			}
			return m, nil
		case "enter":
			if m.input == "" {
				break
			}
			userMsg := m.input
			m.input = ""
			m.cursor = 0
			m.history = append(m.history, runner.ConvMessage{Role: "user", Content: userMsg})
			m.lines = append(m.lines, chatLine{role: "user", content: userMsg})
			m.waiting = true
			m.refreshViewport()
			return m, chatSendCmd(m.llm, m.ctx, m.gen, m.systemPrompt+attachmentBlock(m.attached), m.history)
		case "left":
			if m.cursor > 0 {
				m.cursor--
			}
		case "right":
			if m.cursor < runeLen(m.input) {
				m.cursor++
			}
		case "home", "ctrl+a":
			m.cursor = 0
		case "end", "ctrl+e":
			m.cursor = runeLen(m.input)
		case "backspace":
			m.input, m.cursor = runeDeleteBefore(m.input, m.cursor)
		case "delete":
			m.input, m.cursor = runeDeleteAt(m.input, m.cursor)
		default:
			if len(msg.Runes) > 0 {
				m.input, m.cursor = runeInsert(m.input, m.cursor, msg.Runes)
			}
		}

	case chatStreamStartMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		// Start draining.
		m.lines = append(m.lines, chatLine{role: "assistant", content: ""})
		return m, chatDrainCmd(msg.ch, msg.gen)

	case ChatTokenMsg:
		if msg.Gen != m.gen {
			return m, nil
		}
		// Terminal message from chatSendCmd/chatDrainCmd (error or closed
		// channel): finalise the turn through the normal done path.
		if n := len(m.lines); msg.Text != "" && (n == 0 || m.lines[n-1].role != "assistant") {
			m.lines = append(m.lines, chatLine{role: "assistant", content: ""})
		}
		return m.Update(chatRawToken{text: msg.Text, done: true, gen: msg.Gen})

	case chatRawToken:
		if msg.gen != m.gen {
			return m, nil
		}
		if len(m.lines) > 0 {
			last := &m.lines[len(m.lines)-1]
			if last.role == "assistant" {
				last.content += msg.text
			}
		}
		m.refreshViewport()
		if msg.done {
			// Commit assistant message to history.
			if len(m.lines) > 0 {
				last := m.lines[len(m.lines)-1]
				if last.role == "assistant" {
					m.history = append(m.history, runner.ConvMessage{
						Role:    "assistant",
						Content: last.content,
					})
					// Check for revision directive from PM.
					if artifact, feedback := parseReviseDirective(last.content); artifact != "" && m.reviseFn != nil {
						m.revising = true
						m.refreshViewport()
						return m, reviseArtifactCmd(m.reviseFn, artifact, feedback)
					}
				}
			}
			m.waiting = false
			return m, nil
		}
		return m, chatDrainCmd(msg.ch, msg.gen)

	case chatRevisionDoneMsg:
		m.revising = false
		if msg.err != nil {
			m.lines = append(m.lines, chatLine{
				role:    "assistant",
				content: "⚠ revision failed: " + msg.err.Error(),
			})
		} else {
			m.lines = append(m.lines, chatLine{
				role:    "assistant",
				content: "✓ revised " + msg.artifact + " — close chat (Esc) and re-approve the artifact",
			})
		}
		m.waiting = false
		m.refreshViewport()
		return m, nil
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m *ChatModel) refreshViewport() {
	userStyle := lipgloss.NewStyle().Foreground(ui.Text).Bold(true)
	assistantStyle := lipgloss.NewStyle().Foreground(ui.Text)
	dimStyle := lipgloss.NewStyle().Foreground(ui.Muted)

	var sb strings.Builder
	for _, line := range m.lines {
		switch line.role {
		case "user":
			sb.WriteString(renderWrappedChatLine("you › ", line.content, userStyle, lipgloss.NewStyle(), m.vp.Width))
			sb.WriteString("\n\n")
		case "assistant":
			sb.WriteString(renderMarkdownChatLine("ai › ", line.content, dimStyle, assistantStyle, m.vp.Width))
			sb.WriteString("\n\n")
		}
	}
	m.vp.SetContent(sb.String())
	m.vp.GotoBottom()
}

func (m *ChatModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.vp.Width = w - 4
	m.vp.Height = h - 6
	if m.vp.Height < 3 {
		m.vp.Height = 3
	}
	m.refreshViewport()
}

func (m ChatModel) View() string {
	if m.picking {
		return strings.Join([]string{
			ui.Header("Chat", "attach context", m.width),
			m.picker.View(),
		}, "\n")
	}

	title := "Chat with PM"
	if m.reviseFn == nil {
		title = "Chat"
	}

	hints := []ui.Hint{{Key: "Enter", Label: "send"}, {Key: "Esc", Label: "close"}}
	if m.root != "" {
		hints = append(hints, ui.Hint{Key: "Ctrl+F", Label: "attach .md"})
	}

	status := ""
	switch {
	case m.revising:
		status = "revising artifact…"
	case m.waiting:
		status = "waiting… (Esc cancel)"
	}

	names := make([]string, len(m.attached))
	for i, a := range m.attached {
		names[i] = a.rel
	}
	return chatScreen(chatScreenOpts{
		Width: m.width, Title: title, Attached: names, Body: m.vp.View(), Hints: hints,
		Input: m.input, Cursor: m.cursor, Status: status,
	})
}

// chatScreenOpts describes the shared layout of the chat and negotiate screens.
type chatScreenOpts struct {
	Width    int
	Title    string
	Attached []string // names of attached files, listed under the header
	Body     string   // the transcript viewport
	Hints    []ui.Hint
	Input    string
	Cursor   int
	Status   string // when set, shown in the input box instead of the input
}

// chatScreen lays out header, optional attachment line, transcript, input box
// and hint bar. Every line is at most Width cells wide.
func chatScreen(o chatScreenOpts) string {
	w := o.Width
	dim := lipgloss.NewStyle().Foreground(ui.Muted)

	lines := []string{ui.Header(o.Title, "", w)}
	if len(o.Attached) > 0 {
		lines = append(lines, "  "+dim.Render(ui.Truncate("▪ "+strings.Join(o.Attached, ", "), max(w-2, 1))))
	}
	for _, l := range strings.Split(o.Body, "\n") {
		lines = append(lines, ui.Truncate("  "+l, w))
	}

	inner := max(w-6, 1)
	var view string
	if o.Status != "" {
		view = dim.Render(o.Status)
	} else {
		view = lipgloss.NewStyle().Foreground(ui.Text).Render(renderInputWithCursor(o.Input, o.Cursor))
		if sw := ansi.StringWidth(view); sw > inner {
			view = ansi.TruncateLeft(view, sw-inner, "")
		}
	}
	box := ui.InputBox(view, "", max(w-2, 5), o.Status == "", "")
	for _, l := range strings.Split(box, "\n") {
		lines = append(lines, " "+l)
	}
	lines = append(lines, ui.HintBar(o.Hints, w))
	for i, l := range lines {
		lines[i] = ui.Truncate(l, w)
	}
	return strings.Join(lines, "\n")
}
