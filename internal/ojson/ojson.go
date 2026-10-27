// Package ojson parses JSON into an order-preserving tree. Redfish payloads
// are authored with intent (Id, Name, Status first; Links and Oem last), and
// Go's map-based decoding throws that away.
package ojson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Kind identifies the JSON type of a Value.
type Kind int

const (
	Null Kind = iota
	Bool
	Number
	String
	Array
	Object
)

// Member is one key/value pair of an object, in document order.
type Member struct {
	Key string
	Val *Value
}

// Value is a node in the parsed tree.
type Value struct {
	Kind Kind
	Bool bool
	Num  json.Number
	Str  string
	Arr  []*Value
	Obj  []Member
}

// Parse decodes data into an ordered Value tree.
func Parse(data []byte) (*Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (*Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case nil:
		return &Value{Kind: Null}, nil
	case bool:
		return &Value{Kind: Bool, Bool: t}, nil
	case json.Number:
		return &Value{Kind: Number, Num: t}, nil
	case string:
		return &Value{Kind: String, Str: t}, nil
	case json.Delim:
		switch t {
		case '{':
			v := &Value{Kind: Object}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("expected object key, got %v", kt)
				}
				val, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				v.Obj = append(v.Obj, Member{Key: key, Val: val})
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return v, nil
		case '[':
			v := &Value{Kind: Array}
			for dec.More() {
				val, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				v.Arr = append(v.Arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return v, nil
		}
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

// Get returns the member named key of an object, or nil.
func (v *Value) Get(key string) *Value {
	if v == nil || v.Kind != Object {
		return nil
	}
	for _, m := range v.Obj {
		if m.Key == key {
			return m.Val
		}
	}
	return nil
}

// Path walks a dotted key path (e.g. "Status.Health") and returns the node or
// nil. Keys may themselves contain dots ("@odata.id", "Members@odata.count"):
// at each level the longest matching key wins.
func (v *Value) Path(path string) *Value {
	cur := v
	parts := strings.Split(path, ".")
	for len(parts) > 0 {
		if cur == nil || cur.Kind != Object {
			return nil
		}
		var next *Value
		n := 0
		for i := len(parts); i >= 1; i-- {
			if next = cur.Get(strings.Join(parts[:i], ".")); next != nil {
				n = i
				break
			}
		}
		if next == nil {
			return nil
		}
		cur = next
		parts = parts[n:]
	}
	return cur
}

// Str returns the string value at a dotted path, or "".
func (v *Value) StrAt(path string) string {
	n := v.Path(path)
	if n == nil {
		return ""
	}
	return n.Scalar()
}

// IsScalar reports whether the value is not an array or object.
func (v *Value) IsScalar() bool {
	return v != nil && v.Kind != Array && v.Kind != Object
}

// Len returns the number of members or elements.
func (v *Value) Len() int {
	if v == nil {
		return 0
	}
	switch v.Kind {
	case Array:
		return len(v.Arr)
	case Object:
		return len(v.Obj)
	}
	return 0
}

// Scalar renders a scalar value as plain text.
func (v *Value) Scalar() string {
	if v == nil {
		return ""
	}
	switch v.Kind {
	case Null:
		return "null"
	case Bool:
		return strconv.FormatBool(v.Bool)
	case Number:
		return v.Num.String()
	case String:
		return v.Str
	}
	return ""
}

// Marshal re-encodes the tree, preserving key order. indent "" means compact.
func (v *Value) Marshal(indent string) []byte {
	var b bytes.Buffer
	v.write(&b, indent, 0)
	return b.Bytes()
}

func (v *Value) write(b *bytes.Buffer, indent string, depth int) {
	nl := func(d int) {
		if indent == "" {
			return
		}
		b.WriteByte('\n')
		for i := 0; i < d; i++ {
			b.WriteString(indent)
		}
	}
	switch v.Kind {
	case Null:
		b.WriteString("null")
	case Bool:
		b.WriteString(strconv.FormatBool(v.Bool))
	case Number:
		b.WriteString(v.Num.String())
	case String:
		enc, _ := json.Marshal(v.Str)
		b.Write(enc)
	case Array:
		if len(v.Arr) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range v.Arr {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			e.write(b, indent, depth+1)
		}
		nl(depth)
		b.WriteByte(']')
	case Object:
		if len(v.Obj) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, m := range v.Obj {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			enc, _ := json.Marshal(m.Key)
			b.Write(enc)
			b.WriteString(": ")
			if indent == "" {
				b.Truncate(b.Len() - 1)
			}
			m.Val.write(b, indent, depth+1)
		}
		nl(depth)
		b.WriteByte('}')
	}
}
