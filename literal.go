package cel2filter

import (
	"time"

	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/overloads"
	"cel.dev/cel-go/common/types"
)

// literalOf folds an expression into a Go value when it is a constant the translator understands: a string,
// bool, int or double literal (the parser folds a leading minus into the literal), a timestamp literal left by
// constant folding, -(n) over a number, or timestamp("...") / timestamp(n). Everything else is not a literal.
func literalOf(e ast.Expr) (any, bool) {
	switch e.Kind() {
	case ast.LiteralKind:
		switch v := e.AsLiteral().(type) {
		case types.String:
			return string(v), true
		case types.Bool:
			return bool(v), true
		case types.Int:
			return int64(v), true
		case types.Double:
			return float64(v), true
		case types.Timestamp:
			return v.Time, true
		}
	case ast.CallKind:
		c := e.AsCall()
		args := c.Args()
		if c.IsMemberFunction() || len(args) != 1 {
			return nil, false
		}
		switch c.FunctionName() {
		case operators.Negate:
			switch v, _ := literalOf(args[0]); v := v.(type) {
			case int64:
				return -v, true
			case float64:
				return -v, true
			}
		case overloads.TypeConvertTimestamp:
			switch v, _ := literalOf(args[0]); v := v.(type) {
			case string:
				t, err := time.Parse(time.RFC3339Nano, v)
				if err != nil {
					return nil, false
				}
				return t.UTC(), true
			case int64:
				return time.Unix(v, 0).UTC(), true
			}
		}
	}
	return nil, false
}

// literalString and literalBool narrow literalOf.
func literalString(e ast.Expr) (string, bool) {
	v, ok := literalOf(e)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
