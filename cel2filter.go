// Package cel2filter translates CEL expressions into filter predicates for stores that are not SQL databases:
// vector indexes, search engines, document stores. It is the sibling of github.com/spandigital/cel2sql with a
// different contract. cel2sql translates an expression completely or returns an error, because the database
// is the only evaluator. Here the store is a pre-filter in front of a caller that still holds the compiled
// program, so the translation is partial and sound: whatever the predicate cannot express is dropped from a
// conjunction or voids a disjunction, and the predicate accepts a superset of the documents the expression
// accepts. Result.Exact says when nothing was dropped and the caller may skip re-evaluation.
//
// Convert reads the checked AST: field types come from the type-checker, not from a schema the caller
// repeats. The dialect packages render a Predicate for a particular store; the schema package declares a
// document variable without a Go struct; flatkeys is an Encoder for nested maps stored as flat strings.
package cel2filter

import (
	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/ast"
)

// DefaultMaxNodes bounds the size of the expression tree the translator walks.
const DefaultMaxNodes = 64

// Result is the outcome of Convert. A nil Predicate means no constraint: every document may match. Exact
// means the predicate is equivalent to the expression, not just a superset, on documents whose stored values
// equal what the expression saw.
type Result struct {
	Predicate Predicate
	Exact     bool
}

// Encoder maps a lookup inside a map-typed field to the string a store holds for it. keys is the literal key
// path from the field (["Classification", "Level"] for doc.labels.Classification.Level); value is nil for a
// presence test (has(doc.labels.Classification), "Classification" in doc.labels) and otherwise the compared
// literal (string, bool, int64, float64 or time.Time). ok=false contributes no constraint. See flatkeys.
type Encoder interface {
	Encode(keys []string, value any) (entry string, ok bool)
}

// ConvertOption configures Convert.
type ConvertOption func(*options)

type options struct {
	root     string
	rootSet  bool
	fieldMap map[string]string
	encoders map[string]Encoder
	maxNodes int
}

// WithRoot names the variable the expression is written against. Without it, Convert uses the single variable
// the expression references and fails with ErrRootNotFound when there is none or more than one.
//
// Example:
//
//	cel2filter.Convert(ast, cel2filter.WithRoot("doc"))
func WithRoot(name string) ConvertOption {
	return func(o *options) { o.root, o.rootSet = name, true }
}

// WithFieldMap renames fields on their way to the store. Keys are CEL paths below the root ("author",
// "meta.owner"); values are store keys. Unmapped fields keep the dotted path.
//
// Example:
//
//	cel2filter.WithFieldMap(map[string]string{"meta.owner": "owner_email"})
func WithFieldMap(m map[string]string) ConvertOption {
	return func(o *options) {
		o.fieldMap = make(map[string]string, len(m))
		for k, v := range m {
			o.fieldMap[k] = v
		}
	}
}

// WithEncoder registers how lookups inside a map-typed field (a CEL path below the root) are stored. A map
// field without an encoder contributes no constraint.
//
// Example:
//
//	cel2filter.WithEncoder("labels", flatkeys.Encoder{})
func WithEncoder(field string, enc Encoder) ConvertOption {
	return func(o *options) {
		if o.encoders == nil {
			o.encoders = map[string]Encoder{}
		}
		o.encoders[field] = enc
	}
}

// WithMaxNodes caps the expression tree; a larger expression yields Result{nil, false} without error.
func WithMaxNodes(n int) ConvertOption {
	return func(o *options) { o.maxNodes = n }
}

// Convert derives the store-evaluable predicate of a checked boolean expression.
func Convert(a *cel.Ast, opts ...ConvertOption) (Result, error) {
	if a == nil {
		return Result{}, ErrNilAST
	}
	if !a.IsChecked() {
		return Result{}, ErrUncheckedAST
	}
	if !a.OutputType().IsExactType(cel.BoolType) {
		return Result{}, wrapf(ErrNotBoolean, "result type is %s", a.OutputType())
	}
	o := &options{maxNodes: DefaultMaxNodes}
	for _, opt := range opts {
		opt(o)
	}
	if o.maxNodes <= 0 {
		return Result{}, wrap(ErrInvalidOption, "max nodes must be positive")
	}
	for field, enc := range o.encoders {
		if enc == nil {
			return Result{}, wrapf(ErrInvalidOption, "nil encoder for %q", field)
		}
	}
	nav := ast.NavigateAST(a.NativeRep())
	if !o.rootSet {
		root, err := inferRoot(nav)
		if err != nil {
			return Result{}, err
		}
		o.root = root
	}
	t := &translator{o: o}
	p, exact := t.expr(nav, false)
	if t.nodes > o.maxNodes {
		return Result{}, nil
	}
	return Result{Predicate: p, Exact: exact}, nil
}

// inferRoot returns the one variable the expression references, ignoring comprehension variables.
func inferRoot(nav ast.NavigableExpr) (string, error) {
	bound := map[string]bool{}
	for _, c := range ast.MatchDescendants(nav, ast.KindMatcher(ast.ComprehensionKind)) {
		comp := c.AsComprehension()
		bound[comp.IterVar()] = true
		bound[comp.AccuVar()] = true
		if comp.HasIterVar2() {
			bound[comp.IterVar2()] = true
		}
	}
	seen := map[string]bool{}
	var names []string
	for _, id := range ast.MatchDescendants(nav, ast.KindMatcher(ast.IdentKind)) {
		name := id.AsIdent()
		if bound[name] || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	switch len(names) {
	case 0:
		return "", nil
	case 1:
		return names[0], nil
	}
	return "", wrapf(ErrRootNotFound, "expression references %d variables: %v", len(names), names)
}
