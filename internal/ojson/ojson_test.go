package ojson

import "testing"

func TestParseKeepsOrder(t *testing.T) {
	src := []byte(`{"z":1,"a":{"m":true,"b":null},"list":[1,"two",3.5]}`)
	v, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := v.Obj[0].Key + v.Obj[1].Key + v.Obj[2].Key; got != "zalist" {
		t.Fatalf("order lost: %s", got)
	}
	d, _ := Parse([]byte(`{"@odata.id":"/x","Links":{"Sessions":{"@odata.id":"/s"}},"Members@odata.count":3,"Status.Health":"OK"}`))
	if d.StrAt("@odata.id") != "/x" || d.StrAt("Links.Sessions.@odata.id") != "/s" || d.StrAt("Members@odata.count") != "3" || d.StrAt("Status.Health") != "OK" {
		t.Fatalf("dotted-key path lookup broken")
	}
	if v.StrAt("a.m") != "true" {
		t.Fatalf("path lookup failed")
	}
	if string(v.Marshal("")) != string(src) {
		t.Fatalf("roundtrip mismatch: %s", v.Marshal(""))
	}
	if _, err := Parse([]byte(`{"a":1} x`)); err == nil {
		t.Fatal("expected trailing data error")
	}
}
