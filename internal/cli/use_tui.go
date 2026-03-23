package cli

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	b.WriteString(selectorTitleStyle.Render("Select profile"))
	b.WriteByte('\n')
	b.WriteString(selectorSubtitleStyle.Render("Choose the next active Codex profile"))
	b.WriteString("\n\n")
	for i, row := range m.rows {
		b.WriteString(renderSelectorRow(row, i == m.cursor))
		if i < len(m.rows)-1 {
			b.WriteString("\n\n")
		}
	}
	b.WriteString("\n\n")
	b.WriteString(selectorFooterStyle.Render("↑/↓ move • j/k move • enter switch • q cancel"))
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

var (
	selectorTitleStyle    = lipgloss.NewStyle().Bold(true)
	selectorSubtitleStyle = lipgloss.NewStyle().Faint(true)
	selectorFooterStyle   = lipgloss.NewStyle().Faint(true)
	selectorRowBaseStyle  = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				Padding(0, 1)
	selectorRowActiveStyle = selectorRowBaseStyle.
				BorderForeground(lipgloss.Color("39"))
	selectorRowIdleStyle = selectorRowBaseStyle.
				BorderForeground(lipgloss.Color("240"))
	selectorNameStyle      = lipgloss.NewStyle().Bold(true)
	selectorBadgeBaseStyle = lipgloss.NewStyle().
				Padding(0, 1).
				Bold(true)
	selectorActiveBadgeStyle = selectorBadgeBaseStyle.
					Foreground(lipgloss.Color("0")).
					Background(lipgloss.Color("149"))
	selectorSourceLiveBadgeStyle = selectorBadgeBaseStyle.
					Foreground(lipgloss.Color("0")).
					Background(lipgloss.Color("81"))
	selectorSourceCacheBadgeStyle = selectorBadgeBaseStyle.
					Foreground(lipgloss.Color("255")).
					Background(lipgloss.Color("241"))
	selectorLabelStyle = lipgloss.NewStyle().Faint(true)
)

func renderSelectorRow(row listRow, selected bool) string {
	indicator := "  "
	if selected {
		indicator = "▶ "
	}

	badges := []string{}
	if row.active {
		badges = append(badges, selectorActiveBadgeStyle.Render("ACTIVE"))
	}
	switch row.source {
	case quotaSourceLive:
		badges = append(badges, selectorSourceLiveBadgeStyle.Render("LIVE"))
	default:
		badges = append(badges, selectorSourceCacheBadgeStyle.Render("CACHE"))
	}

	headerParts := []string{indicator + selectorNameStyle.Render(row.name)}
	headerParts = append(headerParts, badges...)
	header := lipgloss.JoinHorizontal(lipgloss.Top, headerParts...)

	left := lipgloss.JoinVertical(
		lipgloss.Left,
		renderSelectorMetric("plan", displayPlan(row.snapshot.Plan)),
		renderSelectorMetric("5H", formatSelectorQuota(row.snapshot.PrimaryUsedPercent)),
		renderSelectorMetric("weekly", formatSelectorQuota(row.snapshot.SecondaryUsedPercent)),
	)

	cardStyle := selectorRowIdleStyle
	if selected {
		cardStyle = selectorRowActiveStyle
	}
	return cardStyle.Render(header + "\n" + left)
}

func renderSelectorMetric(label, value string) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		selectorLabelStyle.Width(8).Render(label),
		value,
	)
}

func formatSelectorQuota(used int) string {
	return padLeftPercent(used) + " used   " + padLeftPercent(remainingPercent(used)) + " left"
}
