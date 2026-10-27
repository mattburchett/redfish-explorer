package tui

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/mattburchett/redfish-explorer/internal/ojson"
)

// Link is a navigable @odata.id discovered in a resource.
type Link struct {
	Label string // JSON path, e.g. Links.Chassis[0]
	Path  string // /redfish/v1/...
	Tail  string // last URL segment, for display
}

// extractLinks walks v and collects every @odata.id that points somewhere
// other than the resource itself, in document order, de-duplicated by path.
func extractLinks(v *ojson.Value, self string) []Link {
	var out []Link
	seen := map[string]bool{self: true, strings.TrimSuffix(self, "/"): true}
	var walk func(n *ojson.Value, label string)
	walk = func(n *ojson.Value, label string) {
		switch n.Kind {
		case ojson.Object:
			for _, m := range n.Obj {
				if m.Key == "@odata.id" && m.Val.Kind == ojson.String {
					p := m.Val.Str
					if !isNavigable(p) {
						continue
					}
					clean := strings.TrimSuffix(p, "/")
					if seen[clean] {
						continue
					}
					seen[clean] = true
					out = append(out, Link{Label: label, Path: p, Tail: tailOf(p)})
					continue
				}
				child := m.Key
				if label != "" {
					child = label + "." + m.Key
				}
				walk(m.Val, child)
			}
		case ojson.Array:
			for i, e := range n.Arr {
				walk(e, label+"["+strconv.Itoa(i)+"]")
			}
		}
	}
	walk(v, "")
	return out
}

func isNavigable(p string) bool {
	if !strings.HasPrefix(p, "/redfish/") {
		return false
	}
	if strings.Contains(p, "#") { // JSON pointer fragments into the same document
		return false
	}
	if strings.HasSuffix(p, "$metadata") || strings.Contains(p, "/$metadata") {
		return false
	}
	return true
}

func tailOf(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if dec, err := url.PathUnescape(p); err == nil {
		return dec
	}
	return p
}

// parentOf returns the parent URL path, never above the service root.
func parentOf(p string) string {
	p = strings.TrimSuffix(p, "/")
	const root = "/redfish/v1"
	if p == root || !strings.HasPrefix(p, root) {
		return root
	}
	if i := strings.LastIndexByte(p, '/'); i > len(root)-1 {
		if i < len(root) {
			return root
		}
		return p[:i]
	}
	return root
}
