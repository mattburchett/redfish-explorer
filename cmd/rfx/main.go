// Command rfx (redfish-explorer) is a terminal UI for walking a BMC's Redfish tree.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattburchett/redfish-explorer/internal/tui"
)

var version = "dev"

// cli is the command line. Every option can also come from the environment
// (BMC_* preferred, REDFISH_* accepted); anything still missing is asked for
// in the login form.
type cli struct {
	Host         string           `short:"h" env:"BMC_HOST,REDFISH_HOST" placeholder:"HOST" help:"BMC host: 10.0.0.5, bmc:8443 or https://bmc."`
	HostArg      string           `arg:"" optional:"" name:"bmc" help:"BMC host (alternative to -h)."`
	User         string           `short:"u" env:"BMC_USER,REDFISH_USER" help:"Username. Leave empty to browse anonymously."`
	Password     string           `short:"p" env:"BMC_PASSWORD,REDFISH_PASSWORD" help:"Password. Prefer the env var or --password-file."`
	PasswordFile string           `env:"BMC_PASSWORD_FILE,REDFISH_PASSWORD_FILE" type:"existingfile" placeholder:"FILE" help:"Read the password from FILE."`
	VerifyTLS    bool             `env:"BMC_VERIFY_TLS,REDFISH_VERIFY_TLS" help:"Verify the BMC's TLS certificate (off by default; BMCs are usually self-signed)."`
	Basic        bool             `env:"BMC_BASIC_AUTH,REDFISH_BASIC_AUTH" help:"Use HTTP basic auth instead of a Redfish session."`
	Path         string           `env:"BMC_PATH,REDFISH_PATH" default:"/redfish/v1" help:"Resource to open first."`
	Timeout      time.Duration    `default:"30s" help:"Per-request timeout."`
	Output       string           `short:"o" enum:"none,path,url,json" default:"none" placeholder:"path|url|json" help:"On quit, print the current resource's path, URL or JSON to stdout, for use by another program. The UI draws on the terminal directly, so this works inside $(...) or a pipe; ctrl+c aborts without printing (exit 130)."`
	Help         helpFlag         `help:"Show help."`
	Version      kong.VersionFlag `short:"V" help:"Print version and exit."`
}

// helpFlag replaces kong's default --help/-h so -h can mean host.
type helpFlag bool

func (helpFlag) BeforeReset(ctx *kong.Context) error {
	err := ctx.PrintUsage(false)
	ctx.Kong.Exit(0)
	return err
}

func main() {
	var c cli
	kong.Parse(&c,
		kong.Name("rfx"),
		kong.Description("Walk a BMC's Redfish API in your terminal. Browse every linked resource, read payloads as an aligned property tree, and edit settings or run Actions behind a confirmation step."),
		kong.NoDefaultHelp(),
		kong.UsageOnError(),
		kong.Vars{"version": "rfx " + version},
		kong.ConfigureHelp(kong.HelpOptions{Compact: true}),
	)

	host := c.Host
	if host == "" {
		host = c.HostArg
	}
	pass := c.Password
	if pass == "" && c.PasswordFile != "" {
		b, err := os.ReadFile(c.PasswordFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "rfx: password-file:", err)
			os.Exit(1)
		}
		pass = strings.TrimRight(string(b), "\r\n")
	}

	opts := tui.Options{
		Host:       host,
		User:       c.User,
		Pass:       pass,
		VerifyTLS:  c.VerifyTLS,
		ForceBasic: c.Basic,
		Timeout:    c.Timeout,
		StartPath:  c.Path,
	}
	// Go straight to the explorer when we have a host and either full
	// credentials or an explicitly anonymous session (host given, no user).
	opts.AutoConnect = host != "" && (c.User == "" || pass != "")

	popts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
	if c.Output != "none" {
		// Keep stdout for the result; talk to the user through the terminal.
		if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			defer tty.Close()
			popts = append(popts, tea.WithInput(tty), tea.WithOutput(tty))
		}
	}
	final, err := tea.NewProgram(tui.New(opts), popts...).Run()
	fm, _ := final.(tui.Model)
	if cl := fm.Client(); cl != nil {
		cl.Logout()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rfx:", err)
		os.Exit(1)
	}
	if c.Output == "none" {
		return
	}
	res := fm.Result()
	if res.Aborted {
		os.Exit(130)
	}
	if res.Path == "" {
		fmt.Fprintln(os.Stderr, "rfx: no resource was loaded")
		os.Exit(1)
	}
	switch c.Output {
	case "path":
		fmt.Println(res.Path)
	case "url":
		fmt.Println(res.URL)
	case "json":
		os.Stdout.Write(res.JSON)
		if len(res.JSON) > 0 && res.JSON[len(res.JSON)-1] != '\n' {
			fmt.Println()
		}
	}
}
