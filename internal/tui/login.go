package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	fHost = iota
	fUser
	fPass
	fVerify
	fCount
)

// loginModel is the connection form shown when parameters are missing or a
// connection attempt failed.
type loginModel struct {
	inputs    [3]textinput.Model
	verifyTLS bool
	focus     int
	err       string
	busy      bool
}

func newLogin(host, user, pass string, verify bool) loginModel {
	var m loginModel
	for i := range m.inputs {
		t := textinput.New()
		t.Prompt = ""
		t.CharLimit = 256
		t.Width = 38
		m.inputs[i] = t
	}
	m.inputs[fHost].Placeholder = "10.0.0.5  or  bmc.example.com:8443"
	m.inputs[fHost].SetValue(host)
	m.inputs[fUser].Placeholder = "root"
	m.inputs[fUser].SetValue(user)
	m.inputs[fPass].Placeholder = "••••••••"
	m.inputs[fPass].EchoMode = textinput.EchoPassword
	m.inputs[fPass].EchoCharacter = '•'
	m.inputs[fPass].SetValue(pass)
	m.verifyTLS = verify
	// Focus the first empty field.
	m.focus = fHost
	for i := fHost; i <= fPass; i++ {
		if strings.TrimSpace(m.inputs[i].Value()) == "" {
			m.focus = i
			break
		}
		if i == fPass {
			m.focus = fPass
		}
	}
	m.setFocus(m.focus)
	return m
}

func (m *loginModel) setFocus(i int) {
	m.focus = (i + fCount) % fCount
	for j := range m.inputs {
		if j == m.focus {
			m.inputs[j].Focus()
		} else {
			m.inputs[j].Blur()
		}
	}
}

func (m *loginModel) values() (host, user, pass string) {
	return strings.TrimSpace(m.inputs[fHost].Value()), strings.TrimSpace(m.inputs[fUser].Value()), m.inputs[fPass].Value()
}

// update returns (model, cmd, submit).
func (m loginModel) update(msg tea.Msg) (loginModel, tea.Cmd, bool) {
	if m.busy {
		return m, nil, false
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "tab", "down":
			m.setFocus(m.focus + 1)
			return m, nil, false
		case "shift+tab", "up":
			m.setFocus(m.focus - 1)
			return m, nil, false
		case "enter":
			if m.focus == fVerify || m.focus == fPass {
				host, _, _ := m.values()
				if host == "" {
					m.err = "host is required"
					m.setFocus(fHost)
					return m, nil, false
				}
				m.err = ""
				return m, nil, true
			}
			m.setFocus(m.focus + 1)
			return m, nil, false
		case " ":
			if m.focus == fVerify {
				m.verifyTLS = !m.verifyTLS
				return m, nil, false
			}
		}
	}
	if m.focus < fVerify {
		var cmd tea.Cmd
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		return m, cmd, false
	}
	return m, nil, false
}

func (m loginModel) view(width, height int, spin string) string {
	var b strings.Builder
	b.WriteString(sTitle.Render("⬢ redfish-explorer"))
	b.WriteString(sDim.Render("  connect to a BMC"))
	b.WriteString("\n\n")
	labels := []string{"Host", "Username", "Password"}
	for i, l := range labels {
		st := sLabel
		if m.focus == i {
			st = sLabelF
		}
		b.WriteString(st.Render(l))
		b.WriteString(m.inputs[i].View())
		b.WriteString("\n")
	}
	box := "[ ]"
	if m.verifyTLS {
		box = "[x]"
	}
	st := sLabel
	if m.focus == fVerify {
		st = sLabelF
		box = sTitle.Render(box)
	}
	b.WriteString(st.Render("TLS"))
	b.WriteString(box + " " + sDim.Render("verify certificate  (space toggles)"))
	b.WriteString("\n\n")
	switch {
	case m.busy:
		b.WriteString(spin + " connecting…")
	case m.err != "":
		b.WriteString(sErr.Render(wrap(m.err, 52)))
	default:
		b.WriteString(sFaint.Render("enter to connect · tab to move · esc/ctrl+c to quit"))
	}
	box2 := sBox.Render(b.String())
	hint := sFaint.Render("leave username empty to browse anonymously")
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center,
		lipgloss.JoinVertical(lipgloss.Center, box2, hint))
}

func wrap(s string, w int) string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			if line != "" && len(line)+1+len(word) > w {
				out = append(out, line)
				line = word
				continue
			}
			if line != "" {
				line += " "
			}
			line += word
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
