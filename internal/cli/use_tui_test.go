package cli

import (
	"strings"
	"testing"

	"codex-switch/internal/quota"
	tea "github.com/charmbracelet/bubbletea"
)

func TestProfileSelectorModelStartsAtActiveProfile(t *testing.T) {
	rows := []listRow{
		{name: "beta", snapshot: snapshotForSelection("team", 3, 31), source: quotaSourceLive},
		{name: "alpha", snapshot: snapshotForSelection("plus", 8, 89), active: true, source: quotaSourceCache},
	}

	m := newProfileSelectorModel(rows)

	if m.cursor != 1 {
		t.Fatalf("cursor = %d, want 1", m.cursor)
	}
}

func TestProfileSelectorModelMovesAndSelects(t *testing.T) {
	rows := []listRow{
		{name: "beta", snapshot: snapshotForSelection("team", 3, 31), source: quotaSourceLive},
		{name: "alpha", snapshot: snapshotForSelection("plus", 8, 89), active: true, source: quotaSourceCache},
	}

	model := newProfileSelectorModel(rows)
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyUp})
	m := updated.(profileSelectorModel)
	if m.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", m.cursor)
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(profileSelectorModel)
	if m.selectedName != "beta" {
		t.Fatalf("selectedName = %q, want beta", m.selectedName)
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
}

func TestProfileSelectorModelCancelsOnQ(t *testing.T) {
	model := newProfileSelectorModel([]listRow{{name: "alpha", snapshot: snapshotForSelection("plus", 8, 89), source: quotaSourceLive}})

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m := updated.(profileSelectorModel)
	if !m.cancelled {
		t.Fatal("cancelled = false, want true")
	}
	if cmd == nil {
		t.Fatal("cmd = nil, want tea.Quit")
	}
}

func TestProfileSelectorModelViewShowsInstructionsAndMarker(t *testing.T) {
	model := newProfileSelectorModel([]listRow{
		{name: "beta", snapshot: snapshotForSelection("team", 3, 31), source: quotaSourceLive},
		{name: "alpha", snapshot: snapshotForSelection("plus", 8, 89), active: true, source: quotaSourceCache},
	})

	view := model.View()
	for _, want := range []string{
		"Select profile",
		"Move with up/down or j/k",
		"used",
		"left",
		"beta",
		"alpha",
		"live",
		"cache",
		"[active]",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("view = %q, want %q", view, want)
		}
	}
}

func snapshotForSelection(plan string, primary, secondary int) quota.Snapshot {
	return quota.Snapshot{
		Plan:                 plan,
		PrimaryUsedPercent:   primary,
		SecondaryUsedPercent: secondary,
	}
}
