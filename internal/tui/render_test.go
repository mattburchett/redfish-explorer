package tui

import (
	"testing"

	"github.com/mattburchett/redfish-explorer/internal/ojson"
)

func TestBuildPatch(t *testing.T) {
	root, _ := ojson.Parse([]byte(`{"Boot":{"Target":"None","X":1},"Arr":[{"a":1},{"b":2}],"Nums":[1,2,3]}`))
	nv := &ojson.Value{Kind: ojson.String, Str: "Pxe"}
	got := string(buildPatch(root, []string{"Boot", "Target"}, nv).Marshal(""))
	if got != `{"Boot":{"Target":"Pxe"}}` {
		t.Fatalf("got %s", got)
	}
	got = string(buildPatch(root, []string{"Arr", "1", "b"}, nv).Marshal(""))
	if got != `{"Arr":[{},{"b":"Pxe"}]}` {
		t.Fatalf("got %s", got)
	}
	n, _ := parseEdit("9", root.Path("Nums").Arr[0])
	got = string(buildPatch(root, []string{"Nums", "1"}, n).Marshal(""))
	if got != `{"Nums":[1,9,3]}` {
		t.Fatalf("got %s", got)
	}
}

func TestParseEdit(t *testing.T) {
	s := &ojson.Value{Kind: ojson.String, Str: "123"}
	if v, _ := parseEdit("456", s); v.Kind != ojson.String {
		t.Fatal("string type not preserved")
	}
	n := &ojson.Value{Kind: ojson.Number, Num: "1"}
	if _, err := parseEdit("abc", n); err == nil {
		t.Fatal("expected number error")
	}
	b := &ojson.Value{Kind: ojson.Bool}
	if v, _ := parseEdit("yes", b); !v.Bool {
		t.Fatal("bool parse")
	}
	if v, _ := parseEdit(`{"a":1}`, nil); v.Kind != ojson.Object {
		t.Fatal("json parse")
	}
}

func TestRenderMeta(t *testing.T) {
	root, _ := ojson.Parse([]byte(`{"Name":"x","Boot":{"T":"None","T@Redfish.AllowableValues":["None","Pxe"]},"L":{"@odata.id":"/redfish/v1/Systems"}}`))
	lines, meta := renderPretty(root, 80, "")
	if len(lines) != len(meta) {
		t.Fatalf("lines %d meta %d", len(lines), len(meta))
	}
	var sawAllow, sawLink bool
	for _, m := range meta {
		if m.PathString() == "Boot.T" && len(m.Allowable) == 2 && m.Editable {
			sawAllow = true
		}
		if m.Link == "/redfish/v1/Systems" {
			sawLink = true
		}
	}
	if !sawAllow || !sawLink {
		t.Fatalf("meta missing: allow=%v link=%v", sawAllow, sawLink)
	}
}
