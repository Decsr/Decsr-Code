package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Decsr/Decsr-Code/internal/runner"
)

// dshDefaultRow is the first row of the dsh model list; it means a blank model,
// so dsh picks its own default.
const dshDefaultRow = "(default)"

// dshListModels lists the DeepSeek API model ids with the key decsr holds. A
// variable so tests need no network.
var dshListModels = func() ([]string, error) {
	models, err := runner.ListProviderModels("deepseek", "")
	if err != nil {
		return nil, err
	}
	return models, nil
}

type dshModelsListMsg struct {
	models []string
	err    error
}

func fetchDshModels() tea.Cmd {
	return func() tea.Msg {
		models, err := dshListModels()
		return dshModelsListMsg{models: models, err: err}
	}
}

// dshRows is the selectable dsh model rows: the default row, the listed ids and
// a saved id that is not among them.
func (m SetupModel) dshRows() []string {
	rows := append([]string{dshDefaultRow}, m.dshModels...)
	if m.dshCustom != "" && !containsString(rows, m.dshCustom) {
		rows = append(rows, m.dshCustom)
	}
	return rows
}

// noteDshCustom remembers a saved model id so it shows as a selected row even
// when the list does not hold it.
func (m *SetupModel) noteDshCustom(id string) {
	if id != "" {
		m.dshCustom = id
	}
}

// dshRowModel is the model a dsh row stands for.
func dshRowModel(row string) string {
	if row == dshDefaultRow {
		return ""
	}
	return row
}

// dshTypedHint is the line above the typed dsh model input.
func dshTypedHint() string {
	if runner.KeySource("deepseek", "") == "" {
		return "no DeepSeek key — run `decsr connect deepseek` or export DEEPSEEK_API_KEY; type a model id, blank for dsh's default:"
	}
	return "no model list; type a model id, blank for dsh's default:"
}

// onDshModels takes the fetched ids. The row that was selected stays selected;
// without a list the saved model goes to the typed input.
func (m *SetupModel) onDshModels(msg dshModelsListMsg) {
	m.dshModelsLoading = false
	prev, agentPrev := "", ""
	if m.provider == providerDsh {
		if rows := m.dshRows(); m.modelIdx < len(rows) {
			prev = dshRowModel(rows[m.modelIdx])
		}
	}
	if m.agentEditing && m.agentEditProv == providerDsh {
		if rows := m.dshRows(); m.agentEditIdx < len(rows) {
			agentPrev = dshRowModel(rows[m.agentEditIdx])
		}
	}
	if msg.err != nil || len(msg.models) == 0 {
		if m.provider == providerDsh {
			m.typed.SetValue(prev)
		}
		if m.agentEditing && m.agentEditProv == providerDsh {
			m.typed.SetValue(agentPrev)
		}
		return
	}
	m.dshModels = msg.models
	if m.provider == providerDsh {
		m.modelIdx = findModelIndex(m.dshRows(), prev)
	}
	if m.agentEditing && m.agentEditProv == providerDsh {
		m.agentEditIdx = findModelIndex(m.dshRows(), agentPrev)
	}
}
