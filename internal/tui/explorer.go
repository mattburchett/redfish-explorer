package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattburchett/redfish-explorer/internal/ojson"
	"github.com/mattburchett/redfish-explorer/internal/redfish"
)

type focusArea int

const (
	focusList focusArea = iota
	focusDetail
)

type inputMode int

const (
	modeNormal inputMode = iota
	modeFilter
	modeSearch
	modeGoto
	modeEdit       // inline value editor (overlay)
	modeRawPatch   // free-form PATCH body (overlay)
	modeActions    // pick an action (overlay)
	modeActionBody // edit action parameters (overlay)
	modeConfirm    // confirm a write (overlay)
	modeNotice     // show a write result (overlay)
	modeSending
)

type navKind int

const (
	navPush navKind = iota
	navBack
	navForward
	navReplace
)

type fetchMsg struct {
	path   string
	res    *redfish.Resource
	err    error
	nav    navKind
	cached bool
}

type pendingOp struct {
	method string
	path   string
	body   []byte
	etag   string
	label  string
}

type writeMsg struct {
	op  pendingOp
	res *redfish.Resource
	err error
}

type action struct {
	Name   string
	Target string
	Params []actionParam
}

type actionParam struct {
	Name   string
	Values []string
}

type navItem struct {
	parent bool
	link   Link
}

const serviceRoot = "/redfish/v1"

type explorer struct {
	client *redfish.Client
	width  int
	height int

	cur     *redfish.Resource
	curPath string
	curErr  string
	hist    []string
	fwd     []string
	cache   map[string]*redfish.Resource

	links  []Link
	items  []navItem // after filtering
	cursor int
	offset int
	filter string

	search   string
	matches  []int
	matchIdx int

	in   textinput.Model
	mode inputMode

	vp       viewport.Model
	raw      bool
	focus    focusArea
	help     bool
	detail   []string
	meta     []lineInfo
	dcur     int // detail cursor line
	summaryN int

	// write flows
	editTarget lineInfo
	allowIdx   int
	actions    []action
	acur       int
	pending    pendingOp
	notice     []string

	loading     bool
	loadingPath string
	status      string
	statusErr   bool
}

func newExplorer(c *redfish.Client, w, h int) explorer {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 8192
	e := explorer{
		client: c,
		width:  w,
		height: h,
		cache:  map[string]*redfish.Resource{},
		in:     in,
		vp:     viewport.New(10, 10),
	}
	e.layout()
	return e
}

// ---- geometry -------------------------------------------------------------

func (e *explorer) leftWidth() int {
	w := e.width * 32 / 100
	return max(26, min(w, 46))
}

func (e *explorer) bodyHeight() int { return max(e.height-3, 4) } // header(2) + status(1)

func (e *explorer) contentWidth() int { return max(e.width-e.leftWidth()-2-2, 10) } // borders + gutter

func (e *explorer) layout() {
	e.vp.Width = e.contentWidth() + 2
	e.vp.Height = max(e.bodyHeight()-2, 1)
	e.rebuildDetail()
}

// ---- data -----------------------------------------------------------------

func (e *explorer) fetch(path string, nav navKind, bust bool) tea.Cmd {
	path = cleanPath(path)
	if !bust {
		if res, ok := e.cache[path]; ok {
			return func() tea.Msg { return fetchMsg{path: path, res: res, nav: nav, cached: true} }
		}
	}
	e.loading = true
	e.loadingPath = path
	c := e.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		res, err := c.Get(ctx, path)
		return fetchMsg{path: path, res: res, err: err, nav: nav}
	}
}

func cleanPath(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.Index(p, "://"); i >= 0 { // full URL pasted
		rest := p[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			p = rest[j:]
		} else {
			p = serviceRoot
		}
	}
	if p == "" {
		return serviceRoot
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if len(p) > 1 && !strings.Contains(p, "?") {
		p = strings.TrimSuffix(p, "/")
	}
	return p
}

func (e *explorer) onFetch(msg fetchMsg) tea.Cmd {
	if msg.res != nil && msg.err == nil {
		e.cache[msg.path] = msg.res
	}
	if e.loading && msg.path != e.loadingPath && !msg.cached {
		return nil // stale response from an abandoned navigation
	}
	e.loading = false
	if msg.res == nil {
		e.setStatus(errString(msg.err), true)
		return nil
	}
	samePath := msg.path == e.curPath
	switch msg.nav {
	case navPush:
		if e.curPath != "" && !samePath {
			e.hist = append(e.hist, e.curPath)
		}
		e.fwd = nil
	case navBack:
		if e.curPath != "" {
			e.fwd = append(e.fwd, e.curPath)
		}
	case navForward:
		if e.curPath != "" {
			e.hist = append(e.hist, e.curPath)
		}
	}
	e.cur = msg.res
	e.curPath = msg.path
	e.curErr = errString(msg.err)
	e.links = nil
	if msg.res.Value != nil {
		e.links = extractLinks(msg.res.Value, msg.path)
	}
	e.actions = extractActions(msg.res.Value)
	if !samePath || msg.nav != navReplace {
		e.filter = ""
		e.search = ""
		e.matches = nil
		e.cursor, e.offset, e.dcur = 0, 0, 0
		e.vp.GotoTop()
	}
	e.applyFilter()
	e.rebuildDetail()
	switch {
	case msg.err != nil:
		e.setStatus(e.curErr, true)
	case msg.cached:
		e.setStatus(fmt.Sprintf("cached · fetched %s ago · R to refresh", time.Since(msg.res.FetchedAt).Round(time.Second)), false)
	case msg.nav == navReplace && samePath:
		e.setStatus("refreshed", false)
		if e.mode == modeNotice && len(e.notice) > 0 {
			e.notice[len(e.notice)-1] = sOK.Render("✓ ") + sDim.Render("reloaded "+e.curPath)
		}
	default:
		e.status = ""
	}
	return nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (e *explorer) applyFilter() {
	e.items = e.items[:0]
	if e.curPath != serviceRoot && e.curPath != "" && e.filter == "" {
		e.items = append(e.items, navItem{parent: true})
	}
	f := strings.ToLower(e.filter)
	for _, l := range e.links {
		if f == "" || strings.Contains(strings.ToLower(l.Label), f) || strings.Contains(strings.ToLower(l.Path), f) {
			e.items = append(e.items, navItem{link: l})
		}
	}
	if e.cursor >= len(e.items) {
		e.cursor = max(len(e.items)-1, 0)
	}
	e.clampOffset()
}

func (e *explorer) clampOffset() {
	vis := e.listHeight()
	if e.cursor < e.offset {
		e.offset = e.cursor
	}
	if e.cursor >= e.offset+vis {
		e.offset = e.cursor - vis + 1
	}
	if e.offset < 0 {
		e.offset = 0
	}
}

func (e *explorer) listHeight() int { return max(e.bodyHeight()-2, 1) }

func (e *explorer) rebuildDetail() {
	w := e.contentWidth()
	var lines []string
	var meta []lineInfo
	if e.cur == nil {
		lines = []string{sFaint.Render("nothing loaded yet")}
		meta = []lineInfo{{}}
	} else {
		lines = summaryLines(e.cur, e.curErr, w)
		e.summaryN = len(lines)
		meta = make([]lineInfo, len(lines))
		var tl []string
		var tm []lineInfo
		if e.raw {
			tl, tm = renderRaw(e.cur.Value, e.cur.Raw, w, e.search)
		} else {
			tl, tm = renderPretty(e.cur.Value, w, e.search)
		}
		lines = append(lines, tl...)
		meta = append(meta, tm...)
	}
	e.detail = lines
	e.meta = meta
	if e.dcur >= len(lines) {
		e.dcur = max(len(lines)-1, 0)
	}
	e.refreshContent()
	e.computeMatches()
}

// refreshContent re-joins the detail lines with the cursor gutter.
func (e *explorer) refreshContent() {
	var b strings.Builder
	showCur := e.focus == focusDetail
	for i, l := range e.detail {
		if showCur && i == e.dcur {
			b.WriteString(sTitle.Render("▸ "))
		} else {
			b.WriteString("  ")
		}
		b.WriteString(l)
		if i < len(e.detail)-1 {
			b.WriteByte('\n')
		}
	}
	e.vp.SetContent(b.String())
}

func (e *explorer) moveDetail(d int) {
	if len(e.detail) == 0 {
		return
	}
	e.dcur = max(0, min(e.dcur+d, len(e.detail)-1))
	e.ensureDetailVisible()
	e.refreshContent()
}

func (e *explorer) ensureDetailVisible() {
	if e.dcur < e.vp.YOffset {
		e.vp.SetYOffset(e.dcur)
	} else if e.dcur >= e.vp.YOffset+e.vp.Height {
		e.vp.SetYOffset(e.dcur - e.vp.Height + 1)
	}
}

func (e *explorer) computeMatches() {
	e.matches = e.matches[:0]
	if e.search == "" {
		return
	}
	t := strings.ToLower(e.search)
	for i, l := range e.detail {
		if strings.Contains(strings.ToLower(ansi.Strip(l)), t) {
			e.matches = append(e.matches, i)
		}
	}
}

func (e *explorer) jumpMatch(delta int) {
	if len(e.matches) == 0 {
		e.setStatus(fmt.Sprintf("no matches for %q", e.search), true)
		return
	}
	e.matchIdx = ((e.matchIdx+delta)%len(e.matches) + len(e.matches)) % len(e.matches)
	e.dcur = e.matches[e.matchIdx]
	e.vp.SetYOffset(max(e.dcur-2, 0))
	e.refreshContent()
	e.setStatus(fmt.Sprintf("match %d/%d for %q", e.matchIdx+1, len(e.matches), e.search), false)
}

func (e *explorer) setStatus(s string, isErr bool) {
	e.status = s
	e.statusErr = isErr
}

func (e *explorer) selected() (navItem, bool) {
	if e.cursor < 0 || e.cursor >= len(e.items) {
		return navItem{}, false
	}
	return e.items[e.cursor], true
}

func (e *explorer) detailLine() lineInfo {
	if e.dcur >= 0 && e.dcur < len(e.meta) {
		return e.meta[e.dcur]
	}
	return lineInfo{}
}

// ---- actions --------------------------------------------------------------

func extractActions(v *ojson.Value) []action {
	if v == nil {
		return nil
	}
	acts := v.Get("Actions")
	if acts == nil || acts.Kind != ojson.Object {
		return nil
	}
	var out []action
	var collect func(obj *ojson.Value)
	collect = func(obj *ojson.Value) {
		for _, m := range obj.Obj {
			if m.Key == "Oem" && m.Val.Kind == ojson.Object {
				for _, o := range m.Val.Obj {
					if o.Val.Kind == ojson.Object {
						collect(o.Val)
					}
				}
				continue
			}
			if !strings.HasPrefix(m.Key, "#") || m.Val.Kind != ojson.Object {
				continue
			}
			a := action{Name: strings.TrimPrefix(m.Key, "#"), Target: m.Val.StrAt("target")}
			if a.Target == "" {
				continue
			}
			for _, p := range m.Val.Obj {
				if name, ok := strings.CutSuffix(p.Key, "@Redfish.AllowableValues"); ok && p.Val.Kind == ojson.Array {
					ap := actionParam{Name: name}
					for _, x := range p.Val.Arr {
						ap.Values = append(ap.Values, x.Scalar())
					}
					a.Params = append(a.Params, ap)
				}
			}
			out = append(out, a)
		}
	}
	collect(acts)
	return out
}

func (a action) defaultBody() string {
	if len(a.Params) == 0 {
		return "{}"
	}
	v := &ojson.Value{Kind: ojson.Object}
	for _, p := range a.Params {
		val := ""
		if len(p.Values) > 0 {
			val = p.Values[0]
		}
		v.Obj = append(v.Obj, ojson.Member{Key: p.Name, Val: &ojson.Value{Kind: ojson.String, Str: val}})
	}
	return string(v.Marshal(""))
}

// ---- update ---------------------------------------------------------------

func (e explorer) update(msg tea.Msg) (explorer, tea.Cmd) {
	switch msg := msg.(type) {
	case fetchMsg:
		return e, e.onFetch(msg)
	case writeMsg:
		return e, e.onWrite(msg)
	case tea.MouseMsg:
		if e.mode != modeNormal {
			return e, nil
		}
		return e.onMouse(msg)
	case tea.KeyMsg:
		if e.help {
			e.help = false
			return e, nil
		}
		if e.mode != modeNormal {
			return e.updateMode(msg)
		}
		return e.onKey(msg)
	}
	return e, nil
}

func (e explorer) onMouse(msg tea.MouseMsg) (explorer, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return e, nil
	}
	inList := msg.X < e.leftWidth()
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if inList {
			e.move(-1)
		} else {
			e.vp.LineUp(3)
		}
	case tea.MouseButtonWheelDown:
		if inList {
			e.move(1)
		} else {
			e.vp.LineDown(3)
		}
	case tea.MouseButtonLeft:
		row := msg.Y - 3 // header(2) + border(1)
		if inList {
			e.focus = focusList
			row += e.offset
			if row >= 0 && row < len(e.items) {
				if row == e.cursor {
					return e, e.open()
				}
				e.cursor = row
				e.clampOffset()
			}
		} else {
			e.focus = focusDetail
			row += e.vp.YOffset
			if row >= 0 && row < len(e.detail) {
				if row == e.dcur {
					return e, e.activateDetail()
				}
				e.dcur = row
			}
		}
		e.refreshContent()
	}
	return e, nil
}

func (e *explorer) move(d int) {
	if len(e.items) == 0 {
		return
	}
	e.cursor = max(0, min(e.cursor+d, len(e.items)-1))
	e.clampOffset()
}

func (e *explorer) open() tea.Cmd {
	it, ok := e.selected()
	if !ok {
		return nil
	}
	if it.parent {
		return e.fetch(parentOf(e.curPath), navPush, false)
	}
	return e.fetch(it.link.Path, navPush, false)
}

// activateDetail handles Enter on a detail row: follow a link or edit.
func (e *explorer) activateDetail() tea.Cmd {
	li := e.detailLine()
	if li.Link != "" {
		return e.fetch(li.Link, navPush, false)
	}
	if li.Editable {
		return e.startEdit()
	}
	return nil
}

func (e *explorer) back() tea.Cmd {
	if len(e.hist) == 0 {
		e.setStatus("already at the start of history", false)
		return nil
	}
	p := e.hist[len(e.hist)-1]
	e.hist = e.hist[:len(e.hist)-1]
	return e.fetch(p, navBack, false)
}

func (e *explorer) forward() tea.Cmd {
	if len(e.fwd) == 0 {
		e.setStatus("nothing to go forward to", false)
		return nil
	}
	p := e.fwd[len(e.fwd)-1]
	e.fwd = e.fwd[:len(e.fwd)-1]
	return e.fetch(p, navForward, false)
}

func (e *explorer) setFocus(f focusArea) {
	if e.focus != f {
		e.focus = f
		e.refreshContent()
	}
}

func (e explorer) onKey(msg tea.KeyMsg) (explorer, tea.Cmd) {
	e.status = ""
	k := msg.String()
	switch k {
	case "ctrl+c", "q":
		return e, tea.Quit
	case "?":
		e.help = true
	case "tab":
		if e.focus == focusList {
			e.setFocus(focusDetail)
		} else {
			e.setFocus(focusList)
		}
	case "j", "down":
		if e.focus == focusList {
			e.move(1)
		} else {
			e.moveDetail(1)
		}
	case "k", "up":
		if e.focus == focusList {
			e.move(-1)
		} else {
			e.moveDetail(-1)
		}
	case "J", "ctrl+d":
		if e.focus == focusList {
			e.move(e.listHeight() / 2)
		} else {
			e.moveDetail(e.vp.Height / 2)
		}
	case "K", "ctrl+u":
		if e.focus == focusList {
			e.move(-e.listHeight() / 2)
		} else {
			e.moveDetail(-e.vp.Height / 2)
		}
	case "pgdown", " ":
		e.setFocus(focusDetail)
		e.moveDetail(e.vp.Height)
	case "pgup":
		e.setFocus(focusDetail)
		e.moveDetail(-e.vp.Height)
	case "home":
		if e.focus == focusList {
			e.cursor = 0
			e.clampOffset()
		} else {
			e.moveDetail(-len(e.detail))
		}
	case "end", "G":
		if e.focus == focusList && k == "end" {
			e.cursor = max(len(e.items)-1, 0)
			e.clampOffset()
		} else {
			e.setFocus(focusDetail)
			e.moveDetail(len(e.detail))
		}
	case "enter", "l", "right":
		if e.focus == focusList {
			return e, e.open()
		}
		return e, e.activateDetail()
	case "backspace", "h", "left", "[":
		return e, e.back()
	case "]":
		return e, e.forward()
	case "u":
		if e.curPath != serviceRoot {
			return e, e.fetch(parentOf(e.curPath), navPush, false)
		}
	case "~":
		return e, e.fetch(serviceRoot, navPush, false)
	case "r":
		e.raw = !e.raw
		e.dcur = 0
		e.vp.GotoTop()
		e.rebuildDetail()
	case "R":
		if e.curPath != "" {
			return e, e.fetch(e.curPath, navReplace, true)
		}
	case "g":
		e.mode = modeGoto
		e.in.Placeholder = "/redfish/v1/..."
		e.in.Width = max(e.width-20, 20)
		e.in.SetValue(e.curPath)
		e.in.CursorEnd()
		return e, e.in.Focus()
	case "/":
		e.in.Width = max(e.width-20, 20)
		if e.focus == focusList {
			e.mode = modeFilter
			e.in.Placeholder = "filter links"
			e.in.SetValue(e.filter)
		} else {
			e.mode = modeSearch
			e.in.Placeholder = "search detail"
			e.in.SetValue(e.search)
		}
		e.in.CursorEnd()
		return e, e.in.Focus()
	case "n":
		e.jumpMatch(1)
	case "N":
		e.jumpMatch(-1)
	case "esc":
		if e.filter != "" {
			e.filter = ""
			e.applyFilter()
		} else if e.search != "" {
			e.search = ""
			e.rebuildDetail()
		}
	case "y":
		e.yank()
	case "s":
		e.save()
	case "e":
		e.setFocus(focusDetail)
		return e, e.startEdit()
	case "P":
		if e.cur == nil {
			return e, nil
		}
		e.mode = modeRawPatch
		e.in.Placeholder = `{"Property": "value"}`
		e.in.Width = e.overlayWidth() - 6
		e.in.SetValue("{}")
		e.in.SetCursor(1)
		return e, e.in.Focus()
	case "a":
		if len(e.actions) == 0 {
			e.setStatus("this resource has no Actions", false)
			return e, nil
		}
		e.mode = modeActions
		e.acur = 0
	}
	return e, nil
}

func (e *explorer) startEdit() tea.Cmd {
	li := e.detailLine()
	if !li.Editable || li.Val == nil {
		e.setStatus("not an editable value — move the cursor onto a property", true)
		return nil
	}
	e.editTarget = li
	e.allowIdx = -1
	for i, v := range li.Allowable {
		if v == li.Val.Scalar() {
			e.allowIdx = i
		}
	}
	e.mode = modeEdit
	e.in.Placeholder = "new value"
	e.in.Width = e.overlayWidth() - 6
	e.in.SetValue(li.Val.Scalar())
	e.in.CursorEnd()
	return e.in.Focus()
}

func (e explorer) updateMode(msg tea.KeyMsg) (explorer, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return e, tea.Quit
	}
	switch e.mode {
	case modeSending:
		return e, nil
	case modeNotice:
		e.mode = modeNormal
		e.notice = nil
		return e, nil
	case modeConfirm:
		if k == "y" || k == "Y" {
			return e, e.send()
		}
		e.mode = modeNormal
		e.setStatus("cancelled — nothing was sent", false)
		return e, nil
	case modeActions:
		switch k {
		case "esc", "q":
			e.mode = modeNormal
		case "j", "down":
			e.acur = min(e.acur+1, len(e.actions)-1)
		case "k", "up":
			e.acur = max(e.acur-1, 0)
		case "enter":
			a := e.actions[e.acur]
			e.mode = modeActionBody
			e.in.Placeholder = "{}"
			e.in.Width = e.overlayWidth() - 6
			e.in.SetValue(a.defaultBody())
			e.in.CursorEnd()
			return e, e.in.Focus()
		}
		return e, nil
	}

	// Text-input modes.
	switch k {
	case "esc":
		switch e.mode {
		case modeFilter:
			e.filter = ""
			e.applyFilter()
		case modeActionBody:
			e.mode = modeActions
			e.in.Blur()
			return e, nil
		}
		e.mode = modeNormal
		e.in.Blur()
		return e, nil
	case "tab", "shift+tab":
		if e.mode == modeEdit && len(e.editTarget.Allowable) > 0 {
			n := len(e.editTarget.Allowable)
			if k == "tab" {
				e.allowIdx = (e.allowIdx + 1) % n
			} else {
				e.allowIdx = ((e.allowIdx-1)%n + n) % n
			}
			e.in.SetValue(e.editTarget.Allowable[e.allowIdx])
			e.in.CursorEnd()
			return e, nil
		}
	case "enter":
		val := e.in.Value()
		mode := e.mode
		e.in.Blur()
		e.mode = modeNormal
		switch mode {
		case modeGoto:
			if strings.TrimSpace(val) == "" {
				return e, nil
			}
			return e, e.fetch(val, navPush, false)
		case modeSearch:
			e.search = strings.TrimSpace(val)
			e.matchIdx = -1
			e.rebuildDetail()
			if e.search != "" {
				e.jumpMatch(1)
			}
		case modeFilter:
			e.filter = strings.TrimSpace(val)
			e.applyFilter()
		case modeEdit:
			nv, err := parseEdit(val, e.editTarget.Val)
			if err != nil {
				e.setStatus(err.Error(), true)
				return e, nil
			}
			body := buildPatch(e.cur.Value, e.editTarget.Path, nv)
			e.stage("PATCH", e.curPath, body.Marshal("  "), e.editTarget.PathString()+" → "+nv.Scalar())
		case modeRawPatch:
			v, err := ojson.Parse([]byte(val))
			if err != nil || v.Kind != ojson.Object {
				e.setStatus("PATCH body must be a JSON object", true)
				e.mode = modeRawPatch
				return e, e.in.Focus()
			}
			e.stage("PATCH", e.curPath, v.Marshal("  "), "raw patch")
		case modeActionBody:
			v, err := ojson.Parse([]byte(val))
			if err != nil || v.Kind != ojson.Object {
				e.setStatus("action body must be a JSON object", true)
				e.mode = modeActionBody
				return e, e.in.Focus()
			}
			a := e.actions[e.acur]
			e.stage("POST", a.Target, v.Marshal("  "), a.Name)
		}
		return e, nil
	}
	var cmd tea.Cmd
	e.in, cmd = e.in.Update(msg)
	if e.mode == modeFilter {
		e.filter = e.in.Value()
		e.cursor = 0
		e.applyFilter()
	}
	return e, cmd
}

func (e *explorer) stage(method, path string, body []byte, label string) {
	etag := ""
	if e.cur != nil && method == "PATCH" {
		etag = e.cur.Headers.Get("ETag")
		if etag == "" && e.cur.Value != nil {
			etag = e.cur.Value.StrAt("@odata.etag")
		}
	}
	e.pending = pendingOp{method: method, path: path, body: body, etag: etag, label: label}
	e.mode = modeConfirm
}

func (e *explorer) send() tea.Cmd {
	e.mode = modeSending
	op := e.pending
	c := e.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		var res *redfish.Resource
		var err error
		if op.method == "POST" {
			res, err = c.Post(ctx, op.path, op.body)
		} else {
			res, err = c.Patch(ctx, op.path, op.body, op.etag)
		}
		return writeMsg{op: op, res: res, err: err}
	}
}

func (e *explorer) onWrite(msg writeMsg) tea.Cmd {
	e.mode = modeNotice
	e.notice = e.notice[:0]
	head := msg.op.method + " " + msg.op.path
	ok := msg.err == nil
	switch {
	case msg.res == nil:
		e.notice = append(e.notice, sCrit.Render("✗ "+head), sErr.Render(errString(msg.err)))
	case ok:
		e.notice = append(e.notice, sOK.Render("✓ "+head), sDim.Render(fmt.Sprintf("HTTP %d %s · %s", msg.res.Status, httpText(msg.res.Status), fmtDur(msg.res.Elapsed))))
	default:
		e.notice = append(e.notice, sCrit.Render("✗ "+head), sErr.Render(fmt.Sprintf("HTTP %d %s · %s", msg.res.Status, httpText(msg.res.Status), fmtDur(msg.res.Elapsed))))
	}
	if msg.res != nil {
		for _, m := range redfish.Messages(msg.res.Value) {
			e.notice = append(e.notice, "• "+m)
		}
		if loc := msg.res.Headers.Get("Location"); loc != "" && (msg.res.Status == 202 || msg.res.Status == 201) {
			e.notice = append(e.notice, sDim.Render("Location: ")+sLink.Render(loc))
		}
		if msg.res.Status == 412 {
			e.notice = append(e.notice, sWarn.Render("The resource changed since it was loaded; press R to refresh and try again."))
		}
	}
	if ok && e.curPath != "" {
		e.notice = append(e.notice, "", sFaint.Render("reloading "+e.curPath+"…"))
		return e.fetch(e.curPath, navReplace, true)
	}
	return nil
}

func httpText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 202:
		return "Accepted"
	case 204:
		return "No Content"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 405:
		return "Method Not Allowed"
	case 409:
		return "Conflict"
	case 412:
		return "Precondition Failed"
	case 500:
		return "Internal Server Error"
	case 503:
		return "Service Unavailable"
	}
	return ""
}

func (e *explorer) yank() {
	if e.cur == nil {
		return
	}
	url := e.client.Host() + e.curPath
	if it, ok := e.selected(); ok && !it.parent && e.focus == focusList {
		url = e.client.Host() + it.link.Path
	} else if li := e.detailLine(); e.focus == focusDetail && li.Link != "" {
		url = e.client.Host() + li.Link
	}
	if err := clipboard.WriteAll(url); err != nil {
		e.setStatus("clipboard unavailable: "+err.Error(), true)
		return
	}
	e.setStatus("copied "+url, false)
}

func (e *explorer) save() {
	if e.cur == nil {
		return
	}
	name := strings.Trim(strings.TrimPrefix(e.curPath, serviceRoot), "/")
	if name == "" {
		name = "ServiceRoot"
	}
	name = strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "_", ":", "_").Replace(name)
	host := strings.NewReplacer("https://", "", "http://", "", ":", "_").Replace(e.client.Host())
	fn := fmt.Sprintf("redfish_%s_%s.json", host, name)
	data := e.cur.Raw
	if e.cur.Value != nil {
		data = append(e.cur.Value.Marshal("  "), '\n')
	}
	if err := os.WriteFile(fn, data, 0o600); err != nil {
		e.setStatus("save failed: "+err.Error(), true)
		return
	}
	e.setStatus("saved "+fn, false)
}

// ---- view -----------------------------------------------------------------

func (e explorer) overlayWidth() int { return max(min(e.width-8, 90), 30) }

func (e explorer) view(spin string) string {
	if e.width < 40 || e.height < 10 {
		return "terminal too small"
	}
	header := e.header()
	body := e.body(spin)
	var overlay string
	switch {
	case e.help:
		overlay = helpView()
	case e.mode == modeEdit:
		overlay = e.editOverlay()
	case e.mode == modeRawPatch:
		overlay = e.boxOverlay("Raw PATCH "+e.curPath, []string{sDim.Render("JSON object to send as a merge-patch:"), "", e.in.View()}, "enter continue · esc cancel")
	case e.mode == modeActions:
		overlay = e.actionsOverlay()
	case e.mode == modeActionBody:
		overlay = e.actionBodyOverlay()
	case e.mode == modeConfirm:
		overlay = e.confirmOverlay()
	case e.mode == modeSending:
		overlay = e.boxOverlay("Sending", []string{spin + " " + e.pending.method + " " + e.pending.path}, "")
	case e.mode == modeNotice:
		overlay = e.boxOverlay("Result", e.notice, "press any key")
	}
	if overlay != "" {
		body = clipHeight(lipgloss.Place(e.width, e.bodyHeight(), lipgloss.Center, lipgloss.Center, overlay), e.bodyHeight())
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, e.statusBar(spin))
}

func clipHeight(s string, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func (e explorer) boxOverlay(title string, lines []string, footer string) string {
	w := e.overlayWidth()
	inner := w - 8
	var b strings.Builder
	b.WriteString(sTitle.Render(title) + "\n\n")
	for _, l := range lines {
		for _, seg := range strings.Split(ansi.Wordwrap(l, inner, " "), "\n") {
			b.WriteString(ansi.Truncate(seg, inner, "…") + "\n")
		}
	}
	if footer != "" {
		b.WriteString("\n" + sFaint.Render(footer))
	}
	return sBox.Width(w).Render(strings.TrimRight(b.String(), "\n"))
}

func (e explorer) editOverlay() string {
	t := e.editTarget
	lines := []string{
		sFaint.Render("property  ") + sKey.Render(t.PathString()),
		sFaint.Render("current   ") + styledScalar(t.Val),
	}
	if len(t.Allowable) > 0 {
		var parts []string
		for i, a := range t.Allowable {
			if i == e.allowIdx {
				parts = append(parts, sSel.Render(" "+a+" "))
			} else {
				parts = append(parts, sDim.Render(a))
			}
		}
		lines = append(lines, sFaint.Render("allowed   ")+strings.Join(parts, sFaint.Render(" · ")))
	}
	lines = append(lines, "", sFaint.Render("new value ")+e.in.View())
	footer := "enter continue · esc cancel"
	if len(t.Allowable) > 0 {
		footer = "tab cycles allowed values · " + footer
	}
	return e.boxOverlay("Edit "+typeName(t.Val), lines, footer)
}

func typeName(v *ojson.Value) string {
	if v == nil {
		return ""
	}
	switch v.Kind {
	case ojson.String:
		return "string"
	case ojson.Number:
		return "number"
	case ojson.Bool:
		return "boolean"
	case ojson.Null:
		return "null"
	}
	return ""
}

func styledScalar(v *ojson.Value) string {
	if v == nil {
		return ""
	}
	r := &renderer{width: 1 << 20}
	text, st := r.scalar(v)
	return st.Render(text)
}

func (e explorer) actionsOverlay() string {
	var lines []string
	for i, a := range e.actions {
		mark := "  "
		name := a.Name
		if i == e.acur {
			mark = sTitle.Render("▸ ")
			name = sSel.Render(" " + a.Name + " ")
		}
		lines = append(lines, mark+name+"  "+sFaint.Render(a.Target))
		for _, p := range a.Params {
			lines = append(lines, "     "+sDim.Render(p.Name+": ")+sFaint.Render(strings.Join(p.Values, ", ")))
		}
	}
	return e.boxOverlay("Actions", lines, "↑/↓ choose · enter select · esc close")
}

func (e explorer) actionBodyOverlay() string {
	a := e.actions[e.acur]
	lines := []string{sFaint.Render("POST ") + sLink.Render(a.Target)}
	for _, p := range a.Params {
		lines = append(lines, sDim.Render(p.Name+": ")+sFaint.Render(strings.Join(p.Values, " · ")))
	}
	lines = append(lines, "", sFaint.Render("body ")+e.in.View())
	return e.boxOverlay(a.Name, lines, "enter continue · esc back")
}

func (e explorer) confirmOverlay() string {
	op := e.pending
	lines := []string{
		sWarn.Render(op.method) + " " + sLink.Render(op.path),
		sDim.Render(op.label),
		"",
	}
	bodyLines, _ := renderRaw(mustParse(op.body), op.body, e.overlayWidth()-8, "")
	if len(bodyLines) > 20 {
		bodyLines = append(bodyLines[:20], sFaint.Render(fmt.Sprintf("… %d more lines", len(bodyLines)-20)))
	}
	lines = append(lines, bodyLines...)
	if op.etag != "" {
		lines = append(lines, "", sFaint.Render("If-Match: "+op.etag))
	}
	return e.boxOverlay("Send this request?", lines, sWarn.Render("y")+sFaint.Render(" send · any other key cancels"))
}

func mustParse(b []byte) *ojson.Value {
	v, err := ojson.Parse(b)
	if err != nil {
		return nil
	}
	return v
}

func (e explorer) header() string {
	left := sBarKey.Render(" ⬢ redfish-explorer ")
	who := e.client.Host()
	if u := e.client.User(); u != "" {
		who = u + "@" + strings.TrimPrefix(strings.TrimPrefix(who, "https://"), "http://")
	}
	right := sBarDim.Render(" "+e.client.Mode().String()+" ") + sBar.Render(who+" ")
	gap := e.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		right = ""
		gap = max(e.width-ansi.StringWidth(left), 0)
	}
	return left + sBar.Render(strings.Repeat(" ", gap)) + right + "\n" + e.breadcrumb()
}

func (e explorer) breadcrumb() string {
	p := e.curPath
	if p == "" {
		return " "
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	var parts []string
	for i, s := range segs {
		if i == len(segs)-1 {
			parts = append(parts, sCrumbEnd.Render(s))
		} else {
			parts = append(parts, sCrumb.Render(s))
		}
	}
	sep := sCrumbSep.Render(" ▸ ")
	line := " " + strings.Join(parts, sep)
	for len(parts) > 2 && ansi.StringWidth(line) > e.width {
		parts = parts[1:]
		line = " " + sCrumbSep.Render("… ▸ ") + strings.Join(parts, sep)
	}
	return ansi.Truncate(line, e.width, "…")
}

func (e explorer) body(spin string) string {
	lw := e.leftWidth()
	rw := e.width - lw
	h := e.bodyHeight()

	leftTitle := fmt.Sprintf("Links %d", len(e.links))
	if e.filter != "" {
		leftTitle = fmt.Sprintf("Links %d/%d ⌕ %s", len(e.items), len(e.links), e.filter)
	}
	rightTitle := "Detail"
	if e.raw {
		rightTitle = "Raw JSON"
	}
	if e.search != "" {
		rightTitle += fmt.Sprintf(" ⌕ %s (%d)", e.search, len(e.matches))
	}
	var rightMeta string
	if e.loading {
		rightMeta = spin + " " + e.loadingPath
	} else if e.cur != nil {
		rightMeta = fmt.Sprintf("HTTP %d · %s · %s", e.cur.Status, fmtDur(e.cur.Elapsed), humanBytes(len(e.cur.Raw)))
		if e.vp.TotalLineCount() > e.vp.Height {
			rightMeta += fmt.Sprintf(" · %d%%", int(e.vp.ScrollPercent()*100))
		}
	}

	left := pane(e.listView(lw-2, h-2), lw, h, leftTitle, "", e.focus == focusList)
	right := pane(e.vp.View(), rw, h, rightTitle, rightMeta, e.focus == focusDetail)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// pane draws a bordered box with a title embedded in the top border.
func pane(content string, w, h int, title, meta string, focused bool) string {
	st, ts := sPane, sPaneTitle
	border := cBorder
	if focused {
		st, ts = sPaneFocus, sPaneTitleF
		border = cBorderF
	}
	inner := lipgloss.NewStyle().Width(w - 2).Height(h - 2).MaxHeight(h - 2).Render(content)
	box := st.Render(inner)
	lines := strings.Split(box, "\n")
	bs := lipgloss.NewStyle().Foreground(border)
	t := ts.Render(" " + title + " ")
	m := ""
	if meta != "" {
		m = sDim.Render(" " + meta + " ")
	}
	avail := w - 2
	tw := ansi.StringWidth(t)
	mw := ansi.StringWidth(m)
	if tw+mw+2 > avail {
		m = ""
		mw = 0
		if tw+2 > avail {
			t = ansi.Truncate(t, max(avail-2, 0), "…")
			tw = ansi.StringWidth(t)
		}
	}
	fill := avail - tw - mw
	lines[0] = bs.Render("╭─") + t + bs.Render(strings.Repeat("─", max(fill-1, 0))) + m + bs.Render("╮")
	return strings.Join(lines, "\n")
}

func (e explorer) listView(w, h int) string {
	if len(e.items) == 0 {
		if e.filter != "" {
			return sFaint.Render(" no links match")
		}
		return sFaint.Render(" no links here")
	}
	var b strings.Builder
	end := min(e.offset+h, len(e.items))
	for i := e.offset; i < end; i++ {
		it := e.items[i]
		sel := i == e.cursor
		var primary, secondary string
		if it.parent {
			primary, secondary = "↑ ..", tailOf(parentOf(e.curPath))
		} else {
			l := it.link
			switch {
			case strings.HasPrefix(l.Label, "Members[") || l.Label == "":
				primary, secondary = l.Tail, l.Label
			case l.Label == l.Tail:
				primary = l.Label
			default:
				primary, secondary = l.Label, l.Tail
			}
		}
		marker := "  "
		if sel {
			marker = "▸ "
		}
		primary = ansi.Truncate(primary, max(w-2, 1), "…")
		pw := ansi.StringWidth(primary)
		room := w - 2 - pw - 2
		var line string
		if sel {
			line = sSel.Render(marker + primary)
			if room > 4 && secondary != "" {
				line += sSelDim.Render(padRight("  "+ansi.Truncate(secondary, room, "…"), w-2-pw))
			} else {
				line += sSel.Render(strings.Repeat(" ", max(w-2-pw, 0)))
			}
		} else {
			line = marker + primary
			if room > 4 && secondary != "" {
				line += sFaint.Render("  " + ansi.Truncate(secondary, room, "…"))
			}
		}
		if ansi.StringWidth(line) > w {
			line = ansi.Truncate(line, w, "…")
		}
		b.WriteString(line)
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func (e explorer) statusBar(spin string) string {
	var left string
	switch e.mode {
	case modeGoto:
		left = sBarKey.Render(" goto ") + sBar.Render(" "+e.in.View())
	case modeFilter:
		left = sBarKey.Render(" filter ") + sBar.Render(" "+e.in.View())
	case modeSearch:
		left = sBarKey.Render(" search ") + sBar.Render(" "+e.in.View())
	default:
		switch {
		case e.status != "":
			st := sBar
			if e.statusErr {
				st = sBar.Foreground(cCrit)
			}
			left = st.Render(" " + e.status)
		case e.focus == focusList:
			if it, ok := e.selected(); ok && !it.parent {
				left = sBar.Render(" " + it.link.Path)
			} else {
				left = hints(e.focus)
			}
		default:
			li := e.detailLine()
			switch {
			case li.Link != "":
				left = sBar.Render(" "+li.PathString()+"  ") + sBarDim.Render("↵ open "+li.Link)
			case li.Editable:
				left = sBar.Render(" "+li.PathString()+"  ") + sBarDim.Render("e edit")
			case len(li.Path) > 0:
				left = sBar.Render(" " + li.PathString())
			default:
				left = hints(e.focus)
			}
		}
	}
	right := sBarDim.Render("? help · q quit ")
	if e.loading {
		right = sBar.Render(spin+" loading ") + right
	}
	gap := e.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 0 {
		left = ansi.Truncate(left, max(e.width-ansi.StringWidth(right), 0), "…")
		gap = 0
	}
	return left + sBar.Render(strings.Repeat(" ", gap)) + right
}

func hints(f focusArea) string {
	var pairs [][2]string
	if f == focusList {
		pairs = [][2]string{{"↵", "open"}, {"⌫", "back"}, {"u", "up"}, {"/", "filter"}, {"g", "goto"}, {"a", "actions"}, {"r", "raw"}, {"⇥", "detail"}}
	} else {
		pairs = [][2]string{{"j/k", "move"}, {"↵", "open/edit"}, {"e", "edit"}, {"/", "search"}, {"P", "patch"}, {"y", "copy"}, {"s", "save"}, {"⇥", "links"}}
	}
	var b strings.Builder
	b.WriteString(sBar.Render(" "))
	for _, p := range pairs {
		b.WriteString(sBarKey.Render(p[0]))
		b.WriteString(sBarDim.Render(" " + p[1] + "  "))
	}
	return b.String()
}

func helpView() string {
	nav := [][2]string{
		{"↑/k  ↓/j", "move · J/K ^d/^u half page"},
		{"↵ / l / →", "open link (list) · open or edit (detail)"},
		{"⌫ / h / ←", "back in history   ] forward"},
		{"u", "up to parent   ~ service root"},
		{"g", "go to a path or URL"},
		{"tab", "switch between links and detail"},
		{"/", "filter links · search detail (n/N)"},
		{"esc", "clear filter or search"},
		{"r", "toggle pretty / raw JSON"},
		{"R", "refresh (bypass cache)"},
		{"y", "copy URL   s save JSON"},
	}
	write := [][2]string{
		{"e", "edit the value under the cursor"},
		{"tab", "(while editing) cycle allowed values"},
		{"a", "run an Action (POST)"},
		{"P", "send a raw PATCH body"},
		{"y", "confirm a write · anything else cancels"},
	}
	var b strings.Builder
	b.WriteString(sTitle.Render("Navigate") + "\n")
	for _, r := range nav {
		b.WriteString(sHelpKey.Render(r[0]) + sHelpText.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + sTitle.Render("Change settings") + "\n")
	for _, r := range write {
		b.WriteString(sHelpKey.Render(r[0]) + sHelpText.Render(r[1]) + "\n")
	}
	b.WriteString("\n" + sFaint.Render("q quit · any key closes this"))
	return sBox.Padding(0, 2).Render(b.String())
}

// summaryLines builds the human header above the full tree.
func summaryLines(res *redfish.Resource, errText string, width int) []string {
	var out []string
	emit := func(s string) {
		if ansi.StringWidth(s) > width {
			s = ansi.Truncate(s, width, "…")
		}
		out = append(out, s)
	}
	if errText != "" {
		for i, l := range strings.Split(ansi.Wordwrap(errText, max(width-2, 10), " "), "\n") {
			if i == 0 {
				emit(sErr.Render("✗ " + l))
			} else {
				emit(sErr.Render("  " + l))
			}
		}
		if res.Value == nil {
			return out
		}
		emit("")
	}
	v := res.Value
	if v == nil {
		return out
	}
	name := v.StrAt("Name")
	if name == "" {
		name = tailOf(res.Path)
	}
	title := lipgloss.NewStyle().Bold(true).Render(name)
	if t := shortType(v.StrAt("@odata.type")); t != "" {
		title += "  " + sDim.Render(t)
	}
	emit(title)
	var badges []string
	badge := func(label, val string) {
		if val == "" {
			return
		}
		st := sString
		if s, ok := enumStyle(val); ok {
			st = s
		}
		badges = append(badges, sFaint.Render(label+" ")+st.Render(val))
	}
	if h := v.StrAt("Status.Health"); h != "" {
		badges = append(badges, sFaint.Render("Health ")+healthIcon(h)+" "+styledEnum(h))
	}
	if h := v.StrAt("Status.HealthRollup"); h != "" && h != v.StrAt("Status.Health") {
		badges = append(badges, sFaint.Render("Rollup ")+healthIcon(h)+" "+styledEnum(h))
	}
	badge("State", v.StrAt("Status.State"))
	badge("Power", v.StrAt("PowerState"))
	if m := v.Get("Members"); m != nil {
		badges = append(badges, sFaint.Render("Members ")+sNumber.Render(fmt.Sprint(m.Len())))
	} else if c := v.StrAt("Members@odata.count"); c != "" {
		badges = append(badges, sFaint.Render("Members ")+sNumber.Render(c))
	}
	badge("Model", v.StrAt("Model"))
	badge("Id", v.StrAt("Id"))
	if len(badges) > 0 {
		cur := ""
		for _, bd := range badges {
			if cur != "" && ansi.StringWidth(cur)+ansi.StringWidth(bd)+5 > width {
				emit(cur)
				cur = ""
			}
			if cur != "" {
				cur += sFaint.Render("  ·  ")
			}
			cur += bd
		}
		emit(cur)
	}
	if d := v.StrAt("Description"); d != "" && d != name {
		for _, l := range strings.Split(ansi.Wordwrap(d, max(width, 10), " "), "\n") {
			emit(sDim.Render(l))
		}
	}
	emit(sFaint.Render(strings.Repeat("─", width)))
	return out
}

func styledEnum(s string) string {
	if st, ok := enumStyle(s); ok {
		return st.Render(s)
	}
	return sString.Render(s)
}

// shortType turns "#ComputerSystem.v1_13_0.ComputerSystem" into "ComputerSystem v1_13_0".
func shortType(t string) string {
	t = strings.TrimPrefix(t, "#")
	if t == "" {
		return ""
	}
	parts := strings.Split(t, ".")
	if len(parts) >= 3 {
		return parts[0] + " " + parts[1]
	}
	if len(parts) == 2 {
		return parts[0]
	}
	return t
}

func fmtDur(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	if d < 10*time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(100 * time.Millisecond).String()
}

func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
