package cel2filter

import (
	"math"
	"strings"
	"time"

	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/overloads"
	"cel.dev/cel-go/common/types"
)

// translator walks a checked AST in negation normal form. Every method returns the predicate for its node and
// whether that predicate is exact; (nil, true) is a tautology, (nil, false) a dropped node. Dropping is always
// safe inside a conjunction and voids a disjunction, so the result accepts a superset of the expression.
type translator struct {
	o     *options
	nodes int
}

// fieldRef is a resolved reference to a field of the root variable: a chain of struct selects (the store key)
// and, when the chain enters a map-typed field, the literal keys looked up inside it.
type fieldRef struct {
	path []string // CEL path through struct fields
	key  string   // store key
	keys []string // keys inside a map field; empty for a plain field
	typ  *types.Type
	enc  Encoder // encoder registered for the map field, or nil
}

func (f fieldRef) isMap() bool {
	return f.typ != nil && f.typ.Kind() == types.MapKind && len(f.keys) == 0
}
func (f fieldRef) inMap() bool    { return len(f.keys) > 0 }
func (f fieldRef) isList() bool   { return f.typ != nil && f.typ.Kind() == types.ListKind }
func (f fieldRef) scalar() kind   { return kindOf(f.typ) }
func (f fieldRef) elemKind() kind { return kindOf(f.typ.Parameters()[0]) }

type kind uint8

const (
	kindNone kind = iota
	kindString
	kindBool
	kindInt
	kindDouble
	kindTimestamp
)

func kindOf(t *types.Type) kind {
	if t == nil {
		return kindNone
	}
	switch t.Kind() {
	case types.StringKind:
		return kindString
	case types.BoolKind:
		return kindBool
	case types.IntKind:
		return kindInt
	case types.DoubleKind:
		return kindDouble
	case types.TimestampKind:
		return kindTimestamp
	}
	return kindNone
}

func typeOf(e ast.Expr) *types.Type {
	if n, ok := e.(ast.NavigableExpr); ok {
		return n.Type()
	}
	return types.DynType
}

// resolve reads a field reference: root.a.b, root.m.k1.k2 or root.m["k1"]["k2"], any mix of select and index
// for map keys. Struct selects must come before map keys on the way out from the root.
func (t *translator) resolve(e ast.Expr) (fieldRef, bool) {
	var path, keys []string
	cur := e
	for {
		switch cur.Kind() {
		case ast.IdentKind:
			if cur.AsIdent() != t.o.root {
				return fieldRef{}, false
			}
			reverse(path)
			reverse(keys)
			ref := fieldRef{path: path, keys: keys, typ: typeOf(e)}
			ref.key = strings.Join(path, ".")
			if mapped, ok := t.o.fieldMap[ref.key]; ok {
				ref.key = mapped
			}
			ref.enc = t.o.encoders[strings.Join(path, ".")]
			return ref, len(path) > 0
		case ast.SelectKind:
			s := cur.AsSelect()
			if s.IsTestOnly() {
				return fieldRef{}, false
			}
			switch typeOf(s.Operand()).Kind() {
			case types.StructKind:
				path = append(path, s.FieldName())
			case types.MapKind:
				if len(path) > 0 {
					return fieldRef{}, false // a struct select below a map key is not a field path
				}
				keys = append(keys, s.FieldName())
			default:
				return fieldRef{}, false
			}
			cur = s.Operand()
		case ast.CallKind:
			c := cur.AsCall()
			if c.FunctionName() != operators.Index || len(c.Args()) != 2 || len(path) > 0 {
				return fieldRef{}, false
			}
			if typeOf(c.Args()[0]).Kind() != types.MapKind {
				return fieldRef{}, false
			}
			k, ok := literalString(c.Args()[1])
			if !ok {
				return fieldRef{}, false
			}
			keys = append(keys, k)
			cur = c.Args()[0]
		default:
			return fieldRef{}, false
		}
	}
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func (t *translator) expr(e ast.Expr, neg bool) (Predicate, bool) {
	t.nodes++
	switch e.Kind() {
	case ast.LiteralKind:
		if b, ok := e.AsLiteral().(types.Bool); ok {
			if bool(b) != neg {
				return nil, true // true, or !false
			}
			return nil, false // false: no constraint can express "nothing"
		}
	case ast.SelectKind:
		if e.AsSelect().IsTestOnly() {
			return t.has(e, neg)
		}
		return t.boolField(e, neg)
	case ast.CallKind:
		c := e.AsCall()
		args := c.Args()
		switch c.FunctionName() {
		case operators.LogicalAnd:
			if neg {
				return t.disjunction(args, true)
			}
			return t.conjunction(args, false)
		case operators.LogicalOr:
			if neg {
				return t.conjunction(args, true)
			}
			return t.disjunction(args, false)
		case operators.LogicalNot:
			if len(args) == 1 {
				return t.expr(args[0], !neg)
			}
		case operators.Equals:
			if len(args) == 2 {
				if p, exact, ok := t.size(c.FunctionName(), args[0], args[1], neg); ok {
					return p, exact
				}
				return t.equals(args[0], args[1], neg)
			}
		case operators.NotEquals:
			if len(args) == 2 {
				if p, exact, ok := t.size(c.FunctionName(), args[0], args[1], neg); ok {
					return p, exact
				}
				return t.equals(args[0], args[1], !neg)
			}
		case operators.In:
			if len(args) == 2 {
				return t.in(args[0], args[1], neg)
			}
		case operators.Less, operators.LessEquals, operators.Greater, operators.GreaterEquals:
			if len(args) == 2 {
				if p, exact, ok := t.size(c.FunctionName(), args[0], args[1], neg); ok {
					return p, exact
				}
				return t.compare(c.FunctionName(), args[0], args[1], neg)
			}
		case overloads.StartsWith:
			if c.IsMemberFunction() && len(args) == 1 {
				return t.prefix(c.Target(), args[0], neg)
			}
		}
	}
	return nil, false
}

// conjunction drops the arguments it cannot translate; the rest still bound the result from above.
func (t *translator) conjunction(args []ast.Expr, neg bool) (Predicate, bool) {
	exact := true
	var out []Predicate
	for _, a := range args {
		p, ex := t.expr(a, neg)
		exact = exact && ex
		switch p := p.(type) {
		case nil:
		case And:
			out = append(out, p.Args...) // a && b && c parses as nested calls
		default:
			out = append(out, p)
		}
	}
	switch len(out) {
	case 0:
		return nil, exact
	case 1:
		return out[0], exact
	}
	return And{Args: out}, exact
}

// disjunction needs every argument: a missing one would narrow the result.
func (t *translator) disjunction(args []ast.Expr, neg bool) (Predicate, bool) {
	exact := true
	out := make([]Predicate, 0, len(args))
	for _, a := range args {
		p, ex := t.expr(a, neg)
		exact = exact && ex
		switch p := p.(type) {
		case nil:
			if ex {
				return nil, true // a tautology makes the whole disjunction true
			}
			return nil, false
		case Or:
			out = append(out, p.Args...)
		default:
			out = append(out, p)
		}
	}
	if len(out) == 1 {
		return out[0], exact
	}
	return Or{Args: out}, exact
}

// leaf applies the negation and zero-value rules shared by every comparison. A positive leaf that the zero
// value of the field satisfies is dropped: the expression sees an absent field as "" or 0, a store sees no
// key and matches nothing. A negated leaf is fine either way, because stores negate "value differs or absent".
func (t *translator) leaf(p Predicate, f fieldRef, neg bool) (Predicate, bool) {
	if neg {
		if f.inMap() || f.isList() {
			return nil, false
		}
		return Not{Arg: p}, true
	}
	if !f.inMap() && !f.isList() && Match(p, MapFields{f.key: zeroOf(f.scalar())}) {
		return nil, false
	}
	return p, true
}

func zeroOf(k kind) any {
	switch k {
	case kindString:
		return ""
	case kindBool:
		return false
	case kindInt:
		return int64(0)
	case kindDouble:
		return float64(0)
	case kindTimestamp:
		return time.Time{}
	}
	return nil
}

// boolField translates a bool field used as a predicate.
func (t *translator) boolField(e ast.Expr, neg bool) (Predicate, bool) {
	f, ok := t.resolve(e)
	if !ok || f.inMap() || f.scalar() != kindBool {
		return nil, false
	}
	if neg {
		return Not{Arg: Eq{Field: f.key, Value: true}}, true
	}
	return Eq{Field: f.key, Value: true}, true
}

// has translates has(root.f), has(root.m.k) and has(root.m.k1.k2). The checker types a presence test as bool,
// so the tested field's own type is not on the node; a field with a registered encoder is taken to be a map,
// which has no presence form of its own, and anything else becomes Exists.
func (t *translator) has(e ast.Expr, neg bool) (Predicate, bool) {
	if neg {
		return nil, false
	}
	s := e.AsSelect()
	switch typeOf(s.Operand()).Kind() {
	case types.StructKind:
		var path []string
		if s.Operand().Kind() == ast.IdentKind {
			if s.Operand().AsIdent() != t.o.root {
				return nil, false
			}
		} else {
			f, ok := t.resolve(s.Operand())
			if !ok || f.inMap() {
				return nil, false
			}
			path = f.path
		}
		path = append(append([]string{}, path...), s.FieldName())
		celPath := strings.Join(path, ".")
		key := celPath
		if mapped, ok := t.o.fieldMap[celPath]; ok {
			key = mapped
		}
		// An encoded map is stored as its list of entries, which is empty exactly when the map is, so
		// presence of the map is presence of the stored list.
		return Exists{Field: key}, true
	case types.MapKind:
		f, ok := t.resolve(s.Operand())
		if !ok {
			return nil, false
		}
		return t.encode(f, append(append([]string{}, f.keys...), s.FieldName()), nil)
	}
	return nil, false
}

// encode turns a lookup inside a map field into an equality on the encoded entry. A presence test is exact;
// a value comparison is not, because the stored entry no longer carries the value's type while the
// expression compares typed values (3 == "3" is false in CEL).
func (t *translator) encode(f fieldRef, keys []string, value any) (Predicate, bool) {
	if f.enc == nil {
		return nil, false
	}
	entry, ok := f.enc.Encode(keys, value)
	if !ok {
		return nil, false
	}
	return Eq{Field: f.key, Value: entry}, value == nil
}

func (t *translator) equals(lhs, rhs ast.Expr, neg bool) (Predicate, bool) {
	f, ok := t.resolve(lhs)
	if !ok {
		if f, ok = t.resolve(rhs); !ok {
			return nil, false
		}
		rhs = lhs
	}
	v, ok := literalOf(rhs)
	if !ok {
		return nil, false
	}
	if f.inMap() {
		if neg {
			return nil, false
		}
		return t.encode(f, f.keys, v)
	}
	if f.isMap() || f.isList() {
		return nil, false
	}
	v, ok = coerce(v, f.scalar())
	if !ok {
		return nil, false
	}
	return t.leaf(Eq{Field: f.key, Value: v}, f, neg)
}

// in translates lit in list-field, "k" in map-field, lit in map-entry, and field in [literals].
func (t *translator) in(elem, coll ast.Expr, neg bool) (Predicate, bool) {
	if f, ok := t.resolve(coll); ok {
		v, isLit := literalOf(elem)
		if !isLit {
			return nil, false
		}
		switch {
		case f.isMap():
			s, ok := v.(string)
			if !ok || neg {
				return nil, false
			}
			return t.encode(f, []string{s}, nil)
		case f.inMap():
			if neg {
				return nil, false
			}
			return t.encode(f, f.keys, v)
		case f.isList():
			v, ok := coerce(v, f.elemKind())
			if !ok {
				return nil, false
			}
			return t.leaf(Eq{Field: f.key, Value: v}, f, neg)
		}
		return nil, false
	}
	if coll.Kind() != ast.ListKind {
		return nil, false
	}
	f, ok := t.resolve(elem)
	if !ok || f.isMap() || f.isList() {
		return nil, false
	}
	elems := coll.AsList().Elements()
	if len(elems) == 0 {
		return nil, false
	}
	values := make([]any, 0, len(elems))
	for _, el := range elems {
		v, ok := literalOf(el)
		if !ok {
			return nil, false
		}
		if f.inMap() {
			if neg {
				return nil, false
			}
			if f.enc == nil {
				return nil, false
			}
			entry, ok := f.enc.Encode(f.keys, v)
			if !ok {
				return nil, false
			}
			values = append(values, entry)
			continue
		}
		if v, ok = coerce(v, f.scalar()); !ok {
			return nil, false
		}
		values = append(values, v)
	}
	if f.inMap() {
		return In{Field: f.key, Values: values}, false
	}
	return t.leaf(In{Field: f.key, Values: values}, f, neg)
}

func (t *translator) prefix(target, arg ast.Expr, neg bool) (Predicate, bool) {
	f, ok := t.resolve(target)
	if !ok || f.inMap() || f.isList() || f.scalar() != kindString {
		return nil, false
	}
	s, ok := literalString(arg)
	if !ok {
		return nil, false
	}
	if s == "" {
		return nil, !neg // startsWith("") is always true
	}
	return t.leaf(Prefix{Field: f.key, Value: s}, f, neg)
}

func (t *translator) compare(fn string, lhs, rhs ast.Expr, neg bool) (Predicate, bool) {
	op := map[string]CompareOp{operators.Greater: Gt, operators.GreaterEquals: Gte, operators.Less: Lt, operators.LessEquals: Lte}[fn]
	f, ok := t.resolve(lhs)
	if !ok {
		if f, ok = t.resolve(rhs); !ok {
			return nil, false
		}
		rhs = lhs
		op = map[CompareOp]CompareOp{Gt: Lt, Gte: Lte, Lt: Gt, Lte: Gte}[op]
	}
	if f.inMap() || f.isMap() || f.isList() {
		return nil, false
	}
	v, ok := literalOf(rhs)
	if !ok {
		return nil, false
	}
	k := f.scalar()
	if k != kindInt && k != kindDouble && k != kindTimestamp {
		return nil, false
	}
	// A fractional bound on an integer field is tightened to the next integer, so the store never has to
	// coerce: n > 2.5 is n >= 3, n < 2.5 is n <= 2.
	if d, isDouble := v.(float64); isDouble && k == kindInt {
		if math.IsNaN(d) || math.IsInf(d, 0) || math.Abs(d) > math.MaxInt64 {
			return nil, false
		}
		if d == math.Trunc(d) {
			v = int64(d)
		} else {
			switch op {
			case Gt, Gte:
				op, v = Gte, int64(math.Ceil(d))
			case Lt, Lte:
				op, v = Lte, int64(math.Floor(d))
			}
		}
	}
	if v, ok = coerce(v, k); !ok {
		return nil, false
	}
	return t.leaf(Compare{Field: f.key, Op: op, Value: v}, f, neg)
}

// sizeOf resolves size(x) or x.size() on a list field or an encoded map field, which stores keep as a list
// that is absent when empty.
func (t *translator) sizeOf(e ast.Expr) (fieldRef, bool) {
	if e.Kind() != ast.CallKind {
		return fieldRef{}, false
	}
	c := e.AsCall()
	var arg ast.Expr
	switch {
	case c.FunctionName() != overloads.Size:
		return fieldRef{}, false
	case c.IsMemberFunction() && len(c.Args()) == 0:
		arg = c.Target()
	case !c.IsMemberFunction() && len(c.Args()) == 1:
		arg = c.Args()[0]
	default:
		return fieldRef{}, false
	}
	f, ok := t.resolve(arg)
	collection := f.isList() || f.isMap() && f.enc != nil
	if !ok || f.inMap() || !collection {
		return fieldRef{}, false
	}
	return f, true
}

// size translates a comparison of size(x) with an integer literal. Whatever implies a non-empty collection
// becomes Exists on the field: exactly when it means "not empty" (> 0, >= 1, != 0), as a superset when it asks
// for more (> 2). A comparison an empty collection can satisfy (== 0, < 3) has no positive form and is dropped,
// as is any negation. ok is false when the call is not a size comparison at all.
func (t *translator) size(fn string, lhs, rhs ast.Expr, neg bool) (p Predicate, exact, ok bool) {
	f, isSize := t.sizeOf(lhs)
	if !isSize {
		if f, isSize = t.sizeOf(rhs); !isSize {
			return nil, false, false
		}
		rhs, fn = lhs, flip(fn) // n < size(x) is size(x) > n
	}
	v, isLit := literalOf(rhs)
	n, isInt := v.(int64)
	if !isLit || !isInt {
		return nil, false, true
	}
	if neg {
		// Negating "size compares to n" is a comparison that may admit the empty collection; only the
		// negation of == 0 (that is, != 0) survives.
		switch fn {
		case operators.Equals:
			fn = operators.NotEquals
		case operators.NotEquals:
			fn = operators.Equals
		default:
			return nil, false, true
		}
	}
	var least int64 // the smallest size the comparison accepts; 0 means it accepts the empty collection
	switch fn {
	case operators.Greater:
		least = n + 1
	case operators.GreaterEquals:
		least = n
	case operators.Equals:
		least = n
	case operators.NotEquals:
		if n != 0 {
			return nil, false, true
		}
		least = 1
	default: // < and <= always admit the empty collection
		return nil, false, true
	}
	if least <= 0 {
		return nil, false, true
	}
	return Exists{Field: f.key}, least == 1 && fn != operators.Equals, true
}

// flip mirrors an ordering operator for swapped operands; == and != are symmetric.
func flip(fn string) string {
	switch fn {
	case operators.Greater:
		return operators.Less
	case operators.GreaterEquals:
		return operators.LessEquals
	case operators.Less:
		return operators.Greater
	case operators.LessEquals:
		return operators.GreaterEquals
	}
	return fn
}

// coerce checks a literal against a field kind, converting an int literal for a double field.
func coerce(v any, k kind) (any, bool) {
	switch k {
	case kindString:
		_, ok := v.(string)
		return v, ok
	case kindBool:
		_, ok := v.(bool)
		return v, ok
	case kindInt:
		_, ok := v.(int64)
		return v, ok
	case kindDouble:
		switch n := v.(type) {
		case float64:
			return n, true
		case int64:
			return float64(n), true
		}
	case kindTimestamp:
		_, ok := v.(time.Time)
		return v, ok
	}
	return nil, false
}
