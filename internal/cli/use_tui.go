package cli

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

var errProfileSelectionCancelled = errors.New("profile selection cancelled")

var useIsTerminal = func(file *os.File) bool {
	return file != nil && term.IsTerminal(int(file.Fd()))
}

var useSelectProfile = func(input *os.File, output io.Writer, rows []listRow) (string, error) {
	return selectProfileTUI(input, output, rows)
}

func selectProfileTUI(input *os.File, output io.Writer, rows []listRow) (string, error) {
	if input == nil {
		return "", errors.New("interactive input is unavailable")
	}
	if len(rows) == 0 {
		return "", errors.New("no profiles available")
	}

	program := tea.NewProgram(
		newProfileSelectorModel(rows),
		tea.WithInput(input),
		tea.WithOutput(output),
	)

	finalModel, err := program.Run()
	if err != nil {
		return "", err
	}

	model, ok := finalModel.(profileSelectorModel)
	if !ok {
		return "", errors.New("unexpected selector model type")
	}
	if model.cancelled || model.selectedName == "" {
		return "", errProfileSelectionCancelled
	}
	return model.selectedName, nil
}

type profileSelectorModel struct {
	rows         []listRow
	cursor       int
	selectedName string
	cancelled    bool
}

func newProfileSelectorModel(rows []listRow) profileSelectorModel {
	cursor := 0
	for i, row := range rows {
		if row.active {
			cursor = i
			break
		}
	}

	return profileSelectorModel{
		rows:   rows,
		cursor: cursor,
	}
}

func (m profileSelectorModel) Init() tea.Cmd {
	return nil
}

func (m profileSelectorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			m.cursor = wrapSelection(m.cursor-1, len(m.rows))
		case "down", "j":
			m.cursor = wrapSelection(m.cursor+1, len(m.rows))
		case "enter":
			if len(m.rows) > 0 {
				m.selectedName = m.rows[m.cursor].name
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m profileSelectorModel) View() string {
	var b strings.Builder
	b.WriteString("Select profile\n")
	b.WriteString("Move with up/down or j/k. Press Enter to switch, q to cancel.\n\n")
	for i, row := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}

		active := ""
		if row.active {
			active = " [active]"
		}

		b.WriteString(cursor)
		b.WriteString(row.name)
		b.WriteString("  [")
		b.WriteString(displayPlan(row.snapshot.Plan))
		b.WriteString("] [")
		b.WriteString(string(row.source))
		b.WriteString("]")
		b.WriteString(active)
		b.WriteByte('\n')
		b.WriteString("    5H used ")
		b.WriteString(padLeftPercent(row.snapshot.PrimaryUsedPercent))
		b.WriteString(" | left ")
		b.WriteString(padLeftPercent(remainingPercent(row.snapshot.PrimaryUsedPercent)))
		b.WriteString("    weekly used ")
		b.WriteString(padLeftPercent(row.snapshot.SecondaryUsedPercent))
		b.WriteString(" | left ")
		b.WriteString(padLeftPercent(remainingPercent(row.snapshot.SecondaryUsedPercent)))
		b.WriteByte('\n')
	}
	return b.String()
}

func padLeftPercent(value int) string {
	text := strconv.Itoa(value) + "%"
	if len(text) >= 4 {
		return text
	}
	return strings.Repeat(" ", 4-len(text)) + text
}

func wrapSelection(index, total int) int {
	if total == 0 {
		return 0
	}
	if index < 0 {
		return total - 1
	}
	if index >= total {
		return 0
	}
	return index
}
