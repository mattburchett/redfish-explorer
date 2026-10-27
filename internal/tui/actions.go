package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattburchett/redfish-explorer/internal/ojson"
	"github.com/mattburchett/redfish-explorer/internal/redfish"
)

// action is a Redfish action (POST target) found in a resource's Actions.
type action struct {
	Name     string
	Target   string
	InfoPath string // @Redfish.ActionInfo, when the BMC provides one
	Params   []actionParam
}

// actionParam describes one parameter, from inline @Redfish.AllowableValues
// annotations and/or an ActionInfo resource.
type actionParam struct {
	Name     string
	Type     string // Redfish ActionInfo DataType: String, Number, Boolean, Object, StringArray, ...
	Required bool
	Values   []string // allowable values, if enumerated
	Pattern  string
}

func (p actionParam) isEnum() bool { return len(p.Values) > 0 }

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
			a := action{Name: strings.TrimPrefix(m.Key, "#"), Target: m.Val.StrAt("target"), InfoPath: m.Val.StrAt("@Redfish.ActionInfo")}
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

// mergeActionInfo folds an ActionInfo resource's Parameters into a.
func (a *action) mergeActionInfo(v *ojson.Value) {
	params := v.Get("Parameters")
	if params == nil || params.Kind != ojson.Array {
		return
	}
	for _, pv := range params.Arr {
		name := pv.StrAt("Name")
		if name == "" {
			continue
		}
		idx := -1
		for i, p := range a.Params {
			if p.Name == name {
				idx = i
			}
		}
		if idx < 0 {
			a.Params = append(a.Params, actionParam{Name: name})
			idx = len(a.Params) - 1
		}
		p := &a.Params[idx]
		p.Type = pv.StrAt("DataType")
		p.Required = pv.StrAt("Required") == "true"
		p.Pattern = pv.StrAt("AllowablePattern")
		if len(p.Values) == 0 {
			for _, key := range []string{"AllowableValues", "AllowableNumbers"} {
				if av := pv.Get(key); av != nil && av.Kind == ojson.Array {
					for _, x := range av.Arr {
						p.Values = append(p.Values, x.Scalar())
					}
				}
			}
		}
	}
}

// ---- parameter form -------------------------------------------------------

type formField struct {
	p     actionParam
	value string
	set   bool // false = omitted from the body
}

type actionForm struct {
	act     action
	fields  []formField
	cur     int // index into fields; len(fields) is the Send row
	editing bool
	loading bool // waiting on ActionInfo
	err     string
}

func newActionForm(a action) actionForm {
	f := actionForm{act: a}
	for _, p := range a.Params {
		fld := formField{p: p}
		if strings.EqualFold(p.Type, "Boolean") && len(p.Values) == 0 {
			fld.p.Values = []string{"true", "false"}
		}
		if fld.p.isEnum() && (p.Required || len(a.Params) == 1) {
			fld.value, fld.set = fld.p.Values[0], true
		}
		f.fields = append(f.fields, fld)
	}
	return f
}

// refresh rebuilds the fields after ActionInfo arrived, keeping typed values.
func (f *actionForm) refresh(a action) {
	old := map[string]formField{}
	for _, fld := range f.fields {
		old[fld.p.Name] = fld
	}
	nf := newActionForm(a)
	for i := range nf.fields {
		if o, ok := old[nf.fields[i].p.Name]; ok && o.set {
			nf.fields[i].value, nf.fields[i].set = o.value, true
		}
	}
	nf.cur = min(f.cur, len(nf.fields))
	f.fields, f.act = nf.fields, a
}

func (f *actionForm) field() *formField {
	if f.cur >= 0 && f.cur < len(f.fields) {
		return &f.fields[f.cur]
	}
	return nil
}

func (f *actionForm) onSend() bool { return f.cur == len(f.fields) }

func (f *actionForm) move(d int) {
	f.cur = max(0, min(f.cur+d, len(f.fields)))
	f.err = ""
}

// cycle steps an enumerated field through its allowable values.
func (f *actionForm) cycle(d int) bool {
	fld := f.field()
	if fld == nil || !fld.p.isEnum() {
		return false
	}
	n := len(fld.p.Values)
	idx := -1
	for i, v := range fld.p.Values {
		if fld.set && v == fld.value {
			idx = i
		}
	}
	idx = ((idx+d)%n + n) % n
	fld.value, fld.set = fld.p.Values[idx], true
	f.err = ""
	return true
}

func (f *actionForm) clear() {
	if fld := f.field(); fld != nil {
		fld.value, fld.set = "", false
	}
}

// body builds the POST body, validating required fields and types.
func (f *actionForm) body() (*ojson.Value, error) {
	v := &ojson.Value{Kind: ojson.Object}
	for _, fld := range f.fields {
		if !fld.set {
			if fld.p.Required {
				return nil, fmt.Errorf("%s is required", fld.p.Name)
			}
			continue
		}
		val, err := convertParam(fld.p, fld.value)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", fld.p.Name, err)
		}
		v.Obj = append(v.Obj, ojson.Member{Key: fld.p.Name, Val: val})
	}
	return v, nil
}

// convertParam turns typed text into a JSON value according to the DataType.
func convertParam(p actionParam, text string) (*ojson.Value, error) {
	trim := strings.TrimSpace(text)
	str := func(s string) *ojson.Value { return &ojson.Value{Kind: ojson.String, Str: s} }
	num := func(s string) (*ojson.Value, error) {
		v, err := ojson.Parse([]byte(strings.TrimSpace(s)))
		if err != nil || v.Kind != ojson.Number {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		return v, nil
	}
	switch strings.ToLower(p.Type) {
	case "boolean":
		switch strings.ToLower(trim) {
		case "true", "yes", "on", "1":
			return &ojson.Value{Kind: ojson.Bool, Bool: true}, nil
		case "false", "no", "off", "0":
			return &ojson.Value{Kind: ojson.Bool, Bool: false}, nil
		}
		return nil, fmt.Errorf("%q is not true/false", text)
	case "number":
		return num(trim)
	case "numberarray":
		arr := &ojson.Value{Kind: ojson.Array}
		for _, part := range splitList(trim) {
			n, err := num(part)
			if err != nil {
				return nil, err
			}
			arr.Arr = append(arr.Arr, n)
		}
		return arr, nil
	case "stringarray":
		arr := &ojson.Value{Kind: ojson.Array}
		for _, part := range splitList(trim) {
			arr.Arr = append(arr.Arr, str(part))
		}
		return arr, nil
	case "object", "objectarray":
		v, err := ojson.Parse([]byte(trim))
		if err != nil {
			return nil, fmt.Errorf("expected JSON: %v", err)
		}
		return v, nil
	case "string":
		return str(text), nil
	}
	// Unknown type: keep strings as strings, but accept JSON structures.
	if strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[") {
		if v, err := ojson.Parse([]byte(trim)); err == nil {
			return v, nil
		}
	}
	return str(text), nil
}

func splitList(s string) []string {
	if strings.HasPrefix(s, "[") {
		if v, err := ojson.Parse([]byte(s)); err == nil && v.Kind == ojson.Array {
			var out []string
			for _, e := range v.Arr {
				out = append(out, e.Scalar())
			}
			return out
		}
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ---- ActionInfo fetch -----------------------------------------------------

type actionInfoMsg struct {
	target string
	res    *redfish.Resource
	err    error
}

func (m actionInfoMsg) path() string {
	if m.res != nil {
		return m.res.Path
	}
	return ""
}

func fetchActionInfo(c *redfish.Client, target, path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res, err := c.Get(ctx, path)
		return actionInfoMsg{target: target, res: res, err: err}
	}
}

// typeHint is a short label for a parameter's type.
func typeHint(p actionParam) string {
	t := p.Type
	if t == "" {
		if p.isEnum() {
			return "choice"
		}
		return "text"
	}
	switch strings.ToLower(t) {
	case "string":
		return "text"
	case "stringarray", "numberarray":
		return "list, comma-separated"
	case "object", "objectarray":
		return "JSON"
	}
	return strings.ToLower(t)
}
