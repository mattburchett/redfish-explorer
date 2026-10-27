package tui

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattburchett/redfish-explorer/internal/ojson"
	"github.com/mattburchett/redfish-explorer/internal/redfish"
)

// TestSnapshotFile renders a saved Redfish payload (SNAPSHOT_FILE) for eyeballing.
func TestSnapshotFile(t *testing.T) {
	fn := os.Getenv("SNAPSHOT_FILE")
	if fn == "" {
		t.Skip("SNAPSHOT_FILE not set")
	}
	raw, err := os.ReadFile(fn)
	if err != nil {
		t.Fatal(err)
	}
	v, err := ojson.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := strconv.Atoi(os.Getenv("SNAPSHOT_W"))
	h, _ := strconv.Atoi(os.Getenv("SNAPSHOT_H"))
	if w == 0 {
		w, h = 140, 45
	}
	c, _ := redfish.New(redfish.Config{Host: "https://bmc.example"})
	e := newExplorer(c, w, h)
	res := &redfish.Resource{Path: v.StrAt("@odata.id"), Status: 200, Headers: http.Header{}, Raw: raw, Value: v, Elapsed: 437 * time.Millisecond, FetchedAt: time.Now()}
	e = run(e, func() tea.Msg { return fetchMsg{path: res.Path, res: res, nav: navReplace} })
	e.focus = focusDetail
	e.dcur = e.summaryN + 3
	e.refreshContent()
	checkFrame(t, e.view("⠋"), w, h)
	fmt.Println(e.view("⠋"))
	// Also dump the full pretty rendering so long payloads can be inspected.
	if os.Getenv("SNAPSHOT_FULL") != "" {
		lines, _ := renderPretty(v, w-e.leftWidth()-4, "")
		for _, l := range lines {
			fmt.Println(l)
		}
	}
}
