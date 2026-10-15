package cel2filter

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Predicate is the store-neutral form of the part of an expression a store can evaluate. Leaves name a field
// by its store key and compare it with a typed Go value (string, bool, int64, float64 or time.Time). A leaf on
// a list-valued field is true when any element satisfies it. Dialects render a predicate into their own
// filter syntax; a node a dialect cannot express is treated as true, which keeps the rendering a superset.
type Predicate interface {
	fmt.Stringer
	isPredicate()
}

// And is true when every argument is.
type And struct{ Args []Predicate }

// Or is true when any argument is.
type Or struct{ Args []Predicate }

// Not negates a leaf. The translator only produces it over Eq, In, Prefix and Compare on single-valued fields,
// where a store's negation ("the value differs, or the field is absent") agrees with the expression's.
type Not struct{ Arg Predicate }

// Eq compares a field with a value; on a list field it is true when any element equals Value.
type Eq struct {
	Field string
	Value any
}

// In is true when the field (or any element of a list field) equals one of Values.
type In struct {
	Field  string
	Values []any
}

// Prefix is true when a string field starts with Value.
type Prefix struct{ Field, Value string }

// Exists is true when the field is present and non-empty.
type Exists struct{ Field string }

// Compare orders a numeric or timestamp field against Value (int64, float64 or time.Time).
type Compare struct {
	Field string
	Op    CompareOp
	Value any
}

// CompareOp is the ordering of a Compare leaf.
type CompareOp uint8

// The four orderings.
const (
	Gt CompareOp = iota + 1
	Gte
	Lt
	Lte
)

// String returns the operator as written in CEL.
func (op CompareOp) String() string {
	switch op {
	case Gt:
		return ">"
	case Gte:
		return ">="
	case Lt:
		return "<"
	case Lte:
		return "<="
	}
	return "?"
}

func (And) isPredicate()     {}
func (Or) isPredicate()      {}
func (Not) isPredicate()     {}
func (Eq) isPredicate()      {}
func (In) isPredicate()      {}
func (Prefix) isPredicate()  {}
func (Exists) isPredicate()  {}
func (Compare) isPredicate() {}

func (p And) String() string     { return join("and", p.Args) }
func (p Or) String() string      { return join("or", p.Args) }
func (p Not) String() string     { return "not(" + p.Arg.String() + ")" }
func (p Eq) String() string      { return p.Field + " == " + formatValue(p.Value) }
func (p Prefix) String() string  { return p.Field + " startsWith " + strconv.Quote(p.Value) }
func (p Exists) String() string  { return "has(" + p.Field + ")" }
func (p Compare) String() string { return p.Field + " " + p.Op.String() + " " + formatValue(p.Value) }

func (p In) String() string {
	parts := make([]string, len(p.Values))
	for i, v := range p.Values {
		parts[i] = formatValue(v)
	}
	return p.Field + " in [" + strings.Join(parts, ", ") + "]"
}

func join(op string, args []Predicate) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.String()
	}
	return op + "(" + strings.Join(parts, ", ") + ")"
}

func formatValue(v any) string {
	switch v := v.(type) {
	case string:
		return strconv.Quote(v)
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("%v", v)
}
