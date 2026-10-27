// Package tui is the Bubble Tea front end for redfish-explorer.
package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattburchett/redfish-explorer/internal/redfish"
)

// Options configures the application.
type Options struct {
	Host        string
	User        string
	Pass        string
	VerifyTLS   bool
	ForceBasic  bool
	Timeout     time.Duration
	StartPath   string
	AutoConnect bool // skip the login form when Host (and credentials) are already known
}

type screen int

const (
	screenLogin screen = iota
	screenExplorer
)

type connectedMsg struct {
	client *redfish.Client
	err    error
}

// Model is the root Bubble Tea model.
type Model struct {
	opts    Options
	screen  screen
	login   loginModel
	ex      explorer
	spin    spinner.Model
	width   int
	height  int
	client  *redfish.Client
	splash  bool // auto-connecting: show a one-line splash instead of the form
	aborted bool // quit with ctrl+c
}

// Result describes where the user was when the program exited.
type Result struct {
	Path    string // current resource path, e.g. /redfish/v1/Systems/1
	URL     string // scheme://host + Path
	JSON    []byte // the resource body, re-indented with keys in BMC order
	Aborted bool   // the user pressed ctrl+c
}

// Result returns the final state for callers that want to act on it.
func (m Model) Result() Result {
	r := Result{Aborted: m.aborted}
	if m.screen != screenExplorer || m.ex.cur == nil {
		return r
	}
	r.Path = m.ex.curPath
	r.URL = m.client.Host() + m.ex.curPath
	if m.ex.cur.Value != nil {
		r.JSON = append(m.ex.cur.Value.Marshal("  "), '\n')
	} else {
		r.JSON = m.ex.cur.Raw
	}
	return r
}

// New creates the root model.
func New(opts Options) Model {
	if opts.StartPath == "" {
		opts.StartPath = serviceRoot
	}
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(cAccent)))
	m := Model{
		opts:   opts,
		screen: screenLogin,
		login:  newLogin(opts.Host, opts.User, opts.Pass, opts.VerifyTLS),
		spin:   sp,
	}
	if opts.AutoConnect {
		m.login.busy = true
		m.splash = true
	}
	return m
}

// Client returns the connected client (for logout on exit), or nil.
func (m Model) Client() *redfish.Client { return m.client }

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spin.Tick}
	if m.opts.AutoConnect {
		cmds = append(cmds, connect(m.opts))
	}
	return tea.Batch(cmds...)
}

func connect(o Options) tea.Cmd {
	return func() tea.Msg {
		c, err := redfish.New(redfish.Config{
			Host: o.Host, User: o.User, Pass: o.Pass,
			VerifyTLS: o.VerifyTLS, ForceBasic: o.ForceBasic, Timeout: o.Timeout,
		})
		if err != nil {
			return connectedMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := c.Login(ctx); err != nil {
			return connectedMsg{err: err}
		}
		return connectedMsg{client: c}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.aborted = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.screen == screenExplorer {
			m.ex.width, m.ex.height = msg.Width, msg.Height
			m.ex.layout()
		}
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case connectedMsg:
		m.login.busy = false
		m.splash = false
		if msg.err != nil {
			m.login.err = msg.err.Error()
			m.screen = screenLogin
			return m, nil
		}
		m.client = msg.client
		m.ex = newExplorer(msg.client, m.width, m.height)
		m.screen = screenExplorer
		return m, m.ex.fetch(m.opts.StartPath, navReplace, false)
	}

	switch m.screen {
	case screenLogin:
		if k, ok := msg.(tea.KeyMsg); ok {
			switch k.String() {
			case "ctrl+c", "esc":
				return m, tea.Quit
			case "q":
				if m.splash {
					return m, tea.Quit
				}
			}
		}
		var cmd tea.Cmd
		var submit bool
		m.login, cmd, submit = m.login.update(msg)
		if submit {
			host, user, pass := m.login.values()
			m.opts.Host, m.opts.User, m.opts.Pass, m.opts.VerifyTLS = host, user, pass, m.login.verifyTLS
			m.login.busy = true
			return m, connect(m.opts)
		}
		return m, cmd
	case screenExplorer:
		var cmd tea.Cmd
		m.ex, cmd = m.ex.update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) View() string {
	switch m.screen {
	case screenExplorer:
		return m.ex.view(m.spin.View())
	default:
		if m.splash {
			return m.splashView()
		}
		return m.login.view(m.width, m.height, m.spin.View())
	}
}

func (m Model) splashView() string {
	who := m.opts.Host
	if m.opts.User != "" {
		who = m.opts.User + "@" + who
	}
	body := sTitle.Render("⬢ redfish-explorer") + "\n\n" + m.spin.View() + " connecting to " + sCrumbEnd.Render(who) + sDim.Render(" …") +
		"\n\n" + sFaint.Render("esc to cancel")
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}
