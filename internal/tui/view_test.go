package tui

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattburchett/redfish-explorer/internal/mockbmc"
	"github.com/mattburchett/redfish-explorer/internal/redfish"
)

func setup(t *testing.T, w, h int) (explorer, func()) {
	t.Helper()
	srv := httptest.NewServer(mockbmc.Handler(false, nil))
	c, err := redfish.New(redfish.Config{Host: srv.URL, User: mockbmc.User, Pass: mockbmc.Pass})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := newExplorer(c, w, h)
	return e, srv.Close
}

// run executes a cmd synchronously and feeds resulting messages back.
func run(e explorer, cmd tea.Cmd) explorer {
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			break
		}
		if b, ok := msg.(tea.BatchMsg); ok {
			for _, c := range b {
				e = run(e, c)
			}
			return e
		}
		e, cmd = e.update(msg)
	}
	return e
}

func TestExplorerFlow(t *testing.T) {
	e, done := setup(t, 120, 36)
	defer done()
	e = run(e, e.fetch(serviceRoot, navReplace, false))
	if e.curPath != serviceRoot || len(e.links) == 0 {
		t.Fatalf("root not loaded: %q %d", e.curPath, len(e.links))
	}
	// Open Systems (first link in doc order after Systems key).
	var idx = -1
	for i, it := range e.items {
		if it.link.Path == "/redfish/v1/Systems" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("no Systems link")
	}
	e.cursor = idx
	e = run(e, e.open())
	if e.curPath != "/redfish/v1/Systems" {
		t.Fatalf("curPath = %s", e.curPath)
	}
	if !e.items[0].parent || e.items[1].link.Tail != "1" {
		t.Fatalf("unexpected items: %+v", e.items)
	}
	e.cursor = 1
	e = run(e, e.open())
	if e.curPath != "/redfish/v1/Systems/1" {
		t.Fatalf("curPath = %s", e.curPath)
	}
	if len(e.hist) != 2 {
		t.Fatalf("hist = %v", e.hist)
	}
	frame := e.view("⠋")
	checkFrame(t, frame, 120, 36)
	if os.Getenv("SNAPSHOT") != "" {
		fmt.Println(frame)
	}
	// Search
	e.search = "health"
	e.rebuildDetail()
	if len(e.matches) < 3 {
		t.Fatalf("expected health matches, got %d", len(e.matches))
	}
	// Raw mode
	e.raw = true
	e.rebuildDetail()
	checkFrame(t, e.view("⠋"), 120, 36)
	if os.Getenv("SNAPSHOT") != "" {
		fmt.Println(e.view("⠋"))
	}
	e.raw = false
	// Back
	e = run(e, e.back())
	if e.curPath != "/redfish/v1/Systems" || len(e.fwd) != 1 {
		t.Fatalf("back failed: %s fwd=%v", e.curPath, e.fwd)
	}
	e = run(e, e.forward())
	if e.curPath != "/redfish/v1/Systems/1" {
		t.Fatalf("forward failed: %s", e.curPath)
	}
	// 404 resource still displays.
	e = run(e, e.fetch("/redfish/v1/Missing", navPush, false))
	if e.curPath != "/redfish/v1/Missing" || e.curErr == "" {
		t.Fatalf("404 handling: %s %q", e.curPath, e.curErr)
	}
	checkFrame(t, e.view("⠋"), 120, 36)
	// Narrow terminal.
	e.width, e.height = 80, 24
	e.layout()
	checkFrame(t, e.view("⠋"), 80, 24)
	if os.Getenv("SNAPSHOT") != "" {
		fmt.Println(e.view("⠋"))
	}
	e.help = true
	checkFrame(t, e.view("⠋"), 80, 24)
}

func checkFrame(t *testing.T, frame string, w, h int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Errorf("frame has %d lines, want %d", len(lines), h)
	}
	for i, l := range lines {
		if lw := ansi.StringWidth(l); lw > w {
			t.Errorf("line %d is %d wide (> %d): %q", i, lw, w, ansi.Strip(l))
		}
	}
}

func TestLoginView(t *testing.T) {
	m := New(Options{})
	m.width, m.height = 100, 30
	v := m.View()
	if !strings.Contains(ansi.Strip(v), "Host") {
		t.Fatal("login view missing fields")
	}
	checkFrame(t, v, 100, 30)
}

func TestEditFlow(t *testing.T) {
	e, done := setup(t, 120, 36)
	defer done()
	e = run(e, e.fetch("/redfish/v1/Systems/1", navReplace, false))
	e.focus = focusDetail
	// Find the Boot.BootSourceOverrideTarget line.
	idx := -1
	for i, m := range e.meta {
		if m.PathString() == "Boot.BootSourceOverrideTarget" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("line not found")
	}
	e.dcur = idx
	e = run(e, e.startEdit())
	if e.mode != modeEdit || len(e.editTarget.Allowable) != 5 {
		t.Fatalf("edit mode not entered: %v %v", e.mode, e.editTarget.Allowable)
	}
	// Tab cycles allowed values: Once -> ... value "Pxe" is index 1.
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyTab})
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyTab})
	if e.in.Value() != "Cd" {
		t.Fatalf("tab cycle gave %q", e.in.Value())
	}
	checkFrame(t, e.view("⠋"), 120, 36)
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.mode != modeConfirm || string(e.pending.body) != "{\n  \"Boot\": {\n    \"BootSourceOverrideTarget\": \"Cd\"\n  }\n}" {
		t.Fatalf("confirm not staged: %v %s", e.mode, e.pending.body)
	}
	if os.Getenv("SNAPSHOT") != "" {
		fmt.Println(e.view("⠋"))
	}
	checkFrame(t, e.view("⠋"), 120, 36)
	e, cmd := e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if e.mode != modeSending {
		t.Fatal("not sending")
	}
	e = run(e, cmd) // write + reload
	if e.mode != modeNotice {
		t.Fatalf("mode = %v", e.mode)
	}
	if os.Getenv("SNAPSHOT") != "" {
		fmt.Println(e.view("⠋"))
	}
	checkFrame(t, e.view("⠋"), 120, 36)
	if got := e.cur.Value.StrAt("Boot.BootSourceOverrideTarget"); got != "Cd" {
		t.Fatalf("patch not applied on reload: %q", got)
	}
	// Reject: read-only key via raw patch.
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter}) // close notice
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'P'}})
	e.in.SetValue(`{"Id":"x"}`)
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})
	e, cmd = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	e = run(e, cmd)
	if e.mode != modeNotice || !strings.Contains(ansi.Strip(strings.Join(e.notice, "\n")), "read-only") {
		t.Fatalf("expected read-only error notice, got %v", e.notice)
	}
	// Action: reset -> Off
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if e.mode != modeActions || len(e.actions) != 1 {
		t.Fatalf("actions: %v %v", e.mode, e.actions)
	}
	checkFrame(t, e.view("⠋"), 120, 36)
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})
	e.in.SetValue(`{"ResetType":"ForceOff"}`)
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter})
	if e.pending.method != "POST" || e.pending.path != "/redfish/v1/Systems/1/Actions/ComputerSystem.Reset" {
		t.Fatalf("pending = %+v", e.pending)
	}
	e, cmd = e.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	e = run(e, cmd)
	if got := e.cur.Value.StrAt("PowerState"); got != "Off" {
		t.Fatalf("power state after reset: %q (notice %v)", got, e.notice)
	}
	// Enter on a link row in the detail pane navigates.
	e, _ = e.update(tea.KeyMsg{Type: tea.KeyEnter}) // close notice
	for i, m := range e.meta {
		if m.Link == "/redfish/v1/Systems/1/Processors" {
			e.dcur = i
		}
	}
	e = run(e, e.activateDetail())
	if e.curPath != "/redfish/v1/Systems/1/Processors" {
		t.Fatalf("detail link nav failed: %s", e.curPath)
	}
}

func TestResult(t *testing.T) {
	e, done := setup(t, 120, 36)
	defer done()
	e = run(e, e.fetch("/redfish/v1/Systems/1", navReplace, false))
	m := Model{screen: screenExplorer, ex: e, client: e.client}
	r := m.Result()
	if r.Path != "/redfish/v1/Systems/1" || r.URL != e.client.Host()+"/redfish/v1/Systems/1" || !strings.Contains(string(r.JSON), `"Name": "Mock System One"`) || r.Aborted {
		t.Fatalf("unexpected result: %+v", r)
	}
	m, _ = modelUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !m.Result().Aborted {
		t.Fatal("ctrl+c should mark aborted")
	}
}

func modelUpdate(m Model, msg tea.Msg) (Model, tea.Cmd) {
	nm, cmd := m.Update(msg)
	return nm.(Model), cmd
}
