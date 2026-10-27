package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattburchett/redfish-explorer/internal/ojson"
)

const (
	indentStep  = 2
	maxKeyAlign = 28
)

// lineInfo describes what a rendered line represents so the detail cursor can
// act on it (follow a link, edit a value).
type lineInfo struct {
	Path      []string     // JSON path from the resource root
	Val       *ojson.Value // scalar value on this line, if any
	Editable  bool
	Link      string   // navigable @odata.id, if the value is one
	Allowable []string // sibling Key@Redfish.AllowableValues
}

func (li lineInfo) PathString() string {
	var b strings.Builder
	for i, p := range li.Path {
		if _, err := strconv.Atoi(p); err == nil {
			b.WriteString("[" + p + "]")
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(p)
	}
	return b.String()
}

// renderer turns an ordered JSON tree into styled, width-bounded lines.
type renderer struct {
	width int
	term  string // lowercase search term, "" for none
	lines []string
	meta  []lineInfo
	ctx   lineInfo // attached to every line emitted until cleared
}

// hl applies st to text, with search-term highlighting if active.
func (r *renderer) hl(text string, st lipgloss.Style) string {
	if r.term == "" {
		return st.Render(text)
	}
	lower := strings.ToLower(text)
	idx := strings.Index(lower, r.term)
	if idx < 0 {
		return st.Render(text)
	}
	var b strings.Builder
	for idx >= 0 {
		b.WriteString(st.Render(text[:idx]))
		b.WriteString(sHi.Inherit(st).Render(text[idx : idx+len(r.term)]))
		text = text[idx+len(r.term):]
		lower = lower[idx+len(r.term):]
		idx = strings.Index(lower, r.term)
	}
	b.WriteString(st.Render(text))
	return b.String()
}

func (r *renderer) emit(line string) {
	if ansi.StringWidth(line) > r.width {
		line = ansi.Truncate(line, r.width, "…")
	}
	r.lines = append(r.lines, line)
	r.meta = append(r.meta, r.ctx)
}

// emitWrapped writes prefix + text, wrapping text so continuation lines
// start under the beginning of the value.
func (r *renderer) emitWrapped(prefix string, text string, st lipgloss.Style) {
	pw := ansi.StringWidth(prefix)
	avail := r.width - pw
	if avail < 16 {
		r.emit(prefix)
		prefix = strings.Repeat(" ", min(pw, r.width/3))
		pw = ansi.StringWidth(prefix)
		avail = r.width - pw
	}
	if ansi.StringWidth(text) <= avail {
		r.emit(prefix + r.hl(text, st))
		return
	}
	wrapped := ansi.Wordwrap(text, avail, " /:,;")
	first := true
	for _, seg := range strings.Split(wrapped, "\n") {
		if first {
			r.emit(prefix + r.hl(seg, st))
			first = false
		} else {
			r.emit(strings.Repeat(" ", pw) + r.hl(seg, st))
		}
	}
}

func (r *renderer) scalar(v *ojson.Value) (string, lipgloss.Style) {
	switch v.Kind {
	case ojson.Null:
		return "null", sNull
	case ojson.Bool:
		return v.Scalar(), sBool
	case ojson.Number:
		return v.Num.String(), sNumber
	case ojson.String:
		if v.Str == "" {
			return `""`, sFaint
		}
		if isNavigable(v.Str) {
			return v.Str, sLink
		}
		if st, ok := enumStyle(v.Str); ok {
			return v.Str, st
		}
		return v.Str, sString
	}
	return "", sString
}

func keyStyle(k string) lipgloss.Style {
	if strings.Contains(k, "@odata.") || strings.Contains(k, "@Redfish.") || strings.Contains(k, "@Message.") {
		return sKeyMeta
	}
	return sKey
}

func isMetaKey(k string) bool {
	return strings.HasPrefix(k, "@odata.") || strings.HasPrefix(k, "@Redfish.")
}

// leafCtx builds the lineInfo for a scalar member of parent.
func leafCtx(parent *ojson.Value, key string, path []string, v *ojson.Value) lineInfo {
	li := lineInfo{Path: path, Val: v}
	if v.Kind == ojson.String && isNavigable(v.Str) {
		li.Link = v.Str
	}
	li.Editable = key != "" && !strings.Contains(key, "@") && key != "Id"
	if parent != nil {
		if av := parent.Get(key + "@Redfish.AllowableValues"); av != nil && av.Kind == ojson.Array {
			for _, e := range av.Arr {
				li.Allowable = append(li.Allowable, e.Scalar())
			}
		}
	}
	return li
}

func appendPath(base []string, p string) []string {
	out := make([]string, len(base)+1)
	copy(out, base)
	out[len(base)] = p
	return out
}

// renderPretty renders a resource as an aligned key/value tree.
func renderPretty(v *ojson.Value, width int, term string) ([]string, []lineInfo) {
	r := &renderer{width: max(width, 20), term: strings.ToLower(term)}
	if v == nil {
		r.emit(sFaint.Render("(no body)"))
		return r.lines, r.meta
	}
	if v.Kind != ojson.Object {
		r.node(v, 0)
		return r.lines, r.meta
	}
	// Order: regular properties, then Actions (they have their own key, a),
	// then @odata/@Redfish annotations.
	var main, actions, meta []ojson.Member
	for _, m := range v.Obj {
		switch {
		case isMetaKey(m.Key):
			meta = append(meta, m)
		case m.Key == "Actions" && m.Val.Kind == ojson.Object:
			actions = append(actions, m)
		default:
			main = append(main, m)
		}
	}
	r.members(v, main, 0, nil)
	if len(actions) > 0 {
		r.emit("")
		r.members(v, actions, 0, nil)
	}
	if len(meta) > 0 {
		r.emit("")
		r.emit(sFaint.Render(strings.Repeat("╌", min(r.width, 40))))
		r.members(v, meta, 0, nil)
	}
	return r.lines, r.meta
}

func (r *renderer) members(parent *ojson.Value, ms []ojson.Member, depth int, base []string) {
	ind := strings.Repeat(" ", depth*indentStep)
	kw := 0
	for _, m := range ms {
		if m.Val.IsScalar() || m.Val.Len() == 0 || inlineArray(m.Val) || linkOnly(m.Val) != "" {
			kw = max(kw, ansi.StringWidth(m.Key))
		}
	}
	kw = min(kw, maxKeyAlign)
	for _, m := range ms {
		ks := keyStyle(m.Key)
		path := appendPath(base, m.Key)
		switch {
		case m.Val.IsScalar():
			r.ctx = leafCtx(parent, m.Key, path, m.Val)
			text, st := r.scalar(m.Val)
			r.emitWrapped(ind+r.hl(padRight(m.Key, kw), ks)+" ", text, st)
		case m.Val.Len() == 0:
			r.ctx = lineInfo{Path: path}
			empty := "{}"
			if m.Val.Kind == ojson.Array {
				empty = "[]"
			}
			r.emit(ind + r.hl(padRight(m.Key, kw), ks) + " " + sFaint.Render(empty))
		case inlineArray(m.Val):
			r.ctx = lineInfo{Path: path}
			r.emitInlineArray(ind+r.hl(padRight(m.Key, kw), ks)+" ", ind, m.Val)
		case linkOnly(m.Val) != "":
			link := linkOnly(m.Val)
			r.ctx = lineInfo{Path: appendPath(path, "@odata.id"), Val: m.Val.Get("@odata.id"), Link: link}
			r.emitWrapped(ind+r.hl(padRight(m.Key, kw), ks)+" "+sPunct.Render("→ "), link, sLink)
		case m.Val.Kind == ojson.Object:
			r.ctx = lineInfo{Path: path}
			r.emit(ind + r.hl(m.Key, sSection) + sPunct.Render(summarizeObject(m.Val)))
			r.members(m.Val, m.Val.Obj, depth+1, path)
		case m.Val.Kind == ojson.Array:
			r.ctx = lineInfo{Path: path}
			r.emit(ind + r.hl(m.Key, sSection) + sPunct.Render(fmt.Sprintf("  [%d]", len(m.Val.Arr))))
			r.array(m.Val, depth+1, path)
		}
		r.ctx = lineInfo{}
	}
}

func (r *renderer) array(v *ojson.Value, depth int, base []string) {
	ind := strings.Repeat(" ", depth*indentStep)
	for i, e := range v.Arr {
		path := appendPath(base, strconv.Itoa(i))
		tag := sFaint.Render(strconv.Itoa(i))
		switch {
		case e.IsScalar():
			r.ctx = leafCtx(nil, "", path, e)
			r.ctx.Editable = true
			text, st := r.scalar(e)
			r.emitWrapped(ind+sPunct.Render("- "), text, st)
		case e.Len() == 0:
			r.ctx = lineInfo{Path: path}
			r.emit(ind + sPunct.Render("- ") + sFaint.Render("{}"))
		case linkOnly(e) != "":
			link := linkOnly(e)
			r.ctx = lineInfo{Path: appendPath(path, "@odata.id"), Val: e.Get("@odata.id"), Link: link}
			r.emitWrapped(ind+sPunct.Render("→ "), link, sLink)
		case e.Kind == ojson.Object:
			r.ctx = lineInfo{Path: path}
			r.emit(ind + sPunct.Render("▸ ") + tag + sPunct.Render(summarizeObject(e)))
			r.members(e, e.Obj, depth+1, path)
		case e.Kind == ojson.Array:
			r.ctx = lineInfo{Path: path}
			r.emit(ind + sPunct.Render("▸ ") + tag + sPunct.Render(fmt.Sprintf("  [%d]", len(e.Arr))))
			r.array(e, depth+1, path)
		}
		r.ctx = lineInfo{}
	}
}

func (r *renderer) node(v *ojson.Value, depth int) {
	switch {
	case v.IsScalar():
		text, st := r.scalar(v)
		r.emitWrapped(strings.Repeat(" ", depth*indentStep), text, st)
	case v.Kind == ojson.Object:
		r.members(v, v.Obj, depth, nil)
	case v.Kind == ojson.Array:
		r.array(v, depth, nil)
	}
}

func (r *renderer) emitInlineArray(prefix, ind string, v *ojson.Value) {
	var parts []string
	for _, e := range v.Arr {
		text, st := r.scalar(e)
		parts = append(parts, r.hl(text, st))
	}
	line := prefix + sPunct.Render("[") + strings.Join(parts, sPunct.Render(", ")) + sPunct.Render("]")
	if ansi.StringWidth(line) <= r.width {
		r.emit(line)
		return
	}
	r.emit(strings.TrimRight(prefix, " "))
	base := r.ctx.Path
	for i, e := range v.Arr {
		text, st := r.scalar(e)
		r.ctx = lineInfo{Path: appendPath(base, strconv.Itoa(i)), Val: e, Editable: true}
		r.emitWrapped(ind+strings.Repeat(" ", indentStep)+sPunct.Render("- "), text, st)
	}
}

// inlineArray reports whether an array is short and all-scalar.
func inlineArray(v *ojson.Value) bool {
	if v.Kind != ojson.Array || len(v.Arr) == 0 || len(v.Arr) > 12 {
		return false
	}
	total := 0
	for _, e := range v.Arr {
		if !e.IsScalar() {
			return false
		}
		total += len(e.Scalar()) + 2
	}
	return total <= 80
}

// linkOnly returns the @odata.id when an object is nothing but a reference
// (an @odata.id plus, at most, other @odata.* annotations).
func linkOnly(v *ojson.Value) string {
	if v == nil || v.Kind != ojson.Object || len(v.Obj) == 0 {
		return ""
	}
	id := ""
	for _, m := range v.Obj {
		if m.Key == "@odata.id" && m.Val.Kind == ojson.String {
			id = m.Val.Str
			continue
		}
		if !strings.HasPrefix(m.Key, "@odata.") {
			return ""
		}
	}
	if !isNavigable(id) {
		return ""
	}
	return id
}

// summarizeObject adds a Health dot after a section header when present.
func summarizeObject(v *ojson.Value) string {
	if h := v.StrAt("Health"); h != "" {
		return "  " + healthIcon(h)
	}
	return ""
}

func padRight(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// renderRaw renders colorized, indented JSON.
func renderRaw(v *ojson.Value, raw []byte, width int, term string) ([]string, []lineInfo) {
	r := &renderer{width: max(width, 20), term: strings.ToLower(term)}
	if v == nil {
		for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
			r.emit(l)
		}
		return r.lines, r.meta
	}
	r.rawNode(v, nil, 0, "", "", nil)
	return r.lines, r.meta
}

func (r *renderer) rawNode(v, parent *ojson.Value, depth int, prefix, suffix string, path []string) {
	ind := strings.Repeat(" ", depth*indentStep)
	switch v.Kind {
	case ojson.Object:
		r.ctx = lineInfo{Path: path}
		if len(v.Obj) == 0 {
			r.emit(ind + prefix + sPunct.Render("{}") + suffix)
			return
		}
		r.emit(ind + prefix + sPunct.Render("{"))
		for i, m := range v.Obj {
			comma := ""
			if i < len(v.Obj)-1 {
				comma = sPunct.Render(",")
			}
			k := r.hl(fmt.Sprintf("%q", m.Key), keyStyle(m.Key)) + sPunct.Render(": ")
			r.rawNode(m.Val, v, depth+1, k, comma, appendPath(path, m.Key))
		}
		r.ctx = lineInfo{Path: path}
		r.emit(ind + sPunct.Render("}") + suffix)
	case ojson.Array:
		r.ctx = lineInfo{Path: path}
		if len(v.Arr) == 0 {
			r.emit(ind + prefix + sPunct.Render("[]") + suffix)
			return
		}
		r.emit(ind + prefix + sPunct.Render("["))
		for i, e := range v.Arr {
			comma := ""
			if i < len(v.Arr)-1 {
				comma = sPunct.Render(",")
			}
			r.rawNode(e, v, depth+1, "", comma, appendPath(path, strconv.Itoa(i)))
		}
		r.ctx = lineInfo{Path: path}
		r.emit(ind + sPunct.Render("]") + suffix)
	default:
		key := ""
		if len(path) > 0 {
			key = path[len(path)-1]
		}
		if parent != nil && parent.Kind == ojson.Object {
			r.ctx = leafCtx(parent, key, path, v)
		} else {
			r.ctx = lineInfo{Path: path, Val: v, Editable: true}
		}
		text, st := r.scalar(v)
		if v.Kind == ojson.String {
			text = fmt.Sprintf("%q", v.Str)
		}
		r.emitWrapped(ind+prefix, text, st)
		if suffix != "" && len(r.lines) > 0 {
			r.lines[len(r.lines)-1] += suffix
		}
	}
	r.ctx = lineInfo{}
}

// buildPatch produces the minimal PATCH body that sets path to nv within root.
// Array siblings are sent as {} (objects) or their current value (scalars), per
// the Redfish convention for patching array members.
func buildPatch(root *ojson.Value, path []string, nv *ojson.Value) *ojson.Value {
	if len(path) == 0 {
		return nv
	}
	if root != nil && root.Kind == ojson.Array {
		idx, _ := strconv.Atoi(path[0])
		arr := make([]*ojson.Value, len(root.Arr))
		for i, el := range root.Arr {
			switch {
			case i == idx:
				arr[i] = buildPatch(el, path[1:], nv)
			case el.Kind == ojson.Object || el.Kind == ojson.Array:
				arr[i] = &ojson.Value{Kind: ojson.Object}
			default:
				arr[i] = el
			}
		}
		return &ojson.Value{Kind: ojson.Array, Arr: arr}
	}
	var child *ojson.Value
	if root != nil {
		child = root.Get(path[0])
	}
	return &ojson.Value{Kind: ojson.Object, Obj: []ojson.Member{{Key: path[0], Val: buildPatch(child, path[1:], nv)}}}
}

// parseEdit converts typed text into a value, preserving the original type
// where that makes sense.
func parseEdit(text string, orig *ojson.Value) (*ojson.Value, error) {
	trim := strings.TrimSpace(text)
	if orig != nil {
		switch orig.Kind {
		case ojson.String:
			if len(trim) >= 2 && trim[0] == '"' && trim[len(trim)-1] == '"' {
				if v, err := ojson.Parse([]byte(trim)); err == nil {
					return v, nil
				}
			}
			return &ojson.Value{Kind: ojson.String, Str: text}, nil
		case ojson.Number:
			v, err := ojson.Parse([]byte(trim))
			if err != nil || v.Kind != ojson.Number {
				if trim == "null" {
					return &ojson.Value{Kind: ojson.Null}, nil
				}
				return nil, fmt.Errorf("%q is not a number", text)
			}
			return v, nil
		case ojson.Bool:
			switch strings.ToLower(trim) {
			case "true", "t", "yes", "y", "1", "on":
				return &ojson.Value{Kind: ojson.Bool, Bool: true}, nil
			case "false", "f", "no", "n", "0", "off":
				return &ojson.Value{Kind: ojson.Bool, Bool: false}, nil
			case "null":
				return &ojson.Value{Kind: ojson.Null}, nil
			}
			return nil, fmt.Errorf("%q is not true/false", text)
		}
	}
	if v, err := ojson.Parse([]byte(trim)); err == nil {
		return v, nil
	}
	return &ojson.Value{Kind: ojson.String, Str: text}, nil
}
