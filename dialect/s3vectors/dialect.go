// Package s3vectors renders predicates as Amazon S3 Vectors metadata filters: the JSON document QueryVectors
// takes as its filter ($and, $or, $eq, $ne, $in, $nin, $gt, $gte, $lt, $lte, $startsWith, $exists). Wrap the
// result with document.NewLazyDocument from the AWS SDK.
//
// Metadata values are strings, numbers, booleans and lists, and $eq and $in match any element of a list.
// Timestamps have no native form, so a time.Time value drops its node. Negation is off by default: S3 Vectors
// does not document whether $ne matches a vector without the key, and the expression's semantics need it to.
package s3vectors

import (
	"errors"
	"fmt"
	"time"

	"github.com/spandigital/cel2filter"
	"github.com/spandigital/cel2filter/dialect"
)

// ErrConstraintLimit reports a predicate with more compared values than WithMaxConstraints allows. S3 Vectors
// rejects such a query, so the caller should fall back to an unfiltered one.
var ErrConstraintLimit = errors.New("s3vectors: constraint limit exceeded")

// Option configures a Dialect.
type Option func(*Dialect)

// Dialect renders S3 Vectors metadata filters.
type Dialect struct {
	filterable     func(string) bool
	negation       bool
	maxConstraints int
}

// Result is a rendered filter with its constraint count (every compared value is one constraint; an ENHANCED
// index allows 100 per query).
type Result struct {
	Filter      map[string]any
	Constraints int
	Exact       bool
}

// New returns a dialect; without options every field is filterable, negation is off and there is no cap.
func New(opts ...Option) *Dialect {
	d := &Dialect{filterable: func(string) bool { return true }}
	for _, o := range opts {
		o(d)
	}
	return d
}

// WithFilterable limits rendering to the fields the index can filter on; a leaf on any other field is
// treated as true. Non-filterable metadata keys are fixed when an index is created.
func WithFilterable(f func(field string) bool) Option {
	return func(d *Dialect) {
		if f != nil {
			d.filterable = f
		}
	}
}

// WithNegation renders Not over Eq and In as $ne and $nin. Turn it on only after checking that the index
// matches vectors that lack the key.
func WithNegation(on bool) Option {
	return func(d *Dialect) { d.negation = on }
}

// WithMaxConstraints makes RenderCounted fail with ErrConstraintLimit above n constraints; 0 means no cap.
func WithMaxConstraints(n int) Option {
	return func(d *Dialect) { d.maxConstraints = n }
}

func init() {
	dialect.Register(dialect.S3Vectors, func() dialect.Dialect { return New() })
}

// Name returns dialect.S3Vectors.
func (d *Dialect) Name() dialect.Name { return dialect.S3Vectors }

// Render implements dialect.Dialect.
func (d *Dialect) Render(p cel2filter.Predicate) (map[string]any, bool, error) {
	r, err := d.RenderCounted(p)
	return r.Filter, r.Exact, err
}

// RenderCounted renders p and counts its constraints; on ErrConstraintLimit the count is still filled in.
func (d *Dialect) RenderCounted(p cel2filter.Predicate) (Result, error) {
	if p == nil {
		return Result{Exact: true}, nil
	}
	out, n, exact, err := d.render(p)
	if err != nil {
		return Result{}, err
	}
	if d.maxConstraints > 0 && n > d.maxConstraints {
		return Result{Constraints: n}, fmt.Errorf("%w: %d constraints, limit %d", ErrConstraintLimit, n, d.maxConstraints)
	}
	return Result{Filter: out, Constraints: n, Exact: exact}, nil
}

// Render is a convenience for New(opts...).RenderCounted(p).
func Render(p cel2filter.Predicate, opts ...Option) (Result, error) {
	return New(opts...).RenderCounted(p)
}

func (d *Dialect) render(p cel2filter.Predicate) (out map[string]any, n int, exact bool, err error) {
	switch p := p.(type) {
	case cel2filter.And:
		exact = true
		var args []any
		for _, a := range p.Args {
			r, c, ex, err := d.render(a)
			if err != nil {
				return nil, 0, false, err
			}
			exact = exact && ex
			if r != nil {
				args = append(args, r)
				n += c
			}
		}
		switch len(args) {
		case 0:
			return nil, 0, exact, nil
		case 1:
			return args[0].(map[string]any), n, exact, nil
		}
		return map[string]any{"$and": args}, n, exact, nil
	case cel2filter.Or:
		exact = true
		args := make([]any, 0, len(p.Args))
		for _, a := range p.Args {
			r, c, ex, err := d.render(a)
			if err != nil {
				return nil, 0, false, err
			}
			if r == nil {
				return nil, 0, false, nil
			}
			exact = exact && ex
			args = append(args, r)
			n += c
		}
		if len(args) == 1 {
			return args[0].(map[string]any), n, exact, nil
		}
		return map[string]any{"$or": args}, n, exact, nil
	case cel2filter.Not:
		if !d.negation {
			return nil, 0, false, nil
		}
		switch leaf := p.Arg.(type) {
		case cel2filter.Eq:
			if d.filterable(leaf.Field) && scalar(leaf.Value) {
				return map[string]any{leaf.Field: map[string]any{"$ne": leaf.Value}}, 1, true, nil
			}
		case cel2filter.In:
			if d.filterable(leaf.Field) && len(leaf.Values) > 0 && scalars(leaf.Values) {
				return map[string]any{leaf.Field: map[string]any{"$nin": leaf.Values}}, len(leaf.Values), true, nil
			}
		}
		return nil, 0, false, nil
	case cel2filter.Eq:
		if d.filterable(p.Field) && scalar(p.Value) {
			return map[string]any{p.Field: map[string]any{"$eq": p.Value}}, 1, true, nil
		}
	case cel2filter.In:
		if d.filterable(p.Field) && len(p.Values) > 0 && scalars(p.Values) {
			return map[string]any{p.Field: map[string]any{"$in": p.Values}}, len(p.Values), true, nil
		}
	case cel2filter.Prefix:
		if d.filterable(p.Field) {
			return map[string]any{p.Field: map[string]any{"$startsWith": p.Value}}, 1, true, nil
		}
	case cel2filter.Exists:
		if d.filterable(p.Field) {
			return map[string]any{p.Field: map[string]any{"$exists": true}}, 1, true, nil
		}
	case cel2filter.Compare:
		if d.filterable(p.Field) && number(p.Value) {
			return map[string]any{p.Field: map[string]any{"$" + opName(p.Op): p.Value}}, 1, true, nil
		}
	default:
		return nil, 0, false, fmt.Errorf("%w: predicate %T", dialect.ErrUnsupportedFeature, p)
	}
	return nil, 0, false, nil
}

func opName(op cel2filter.CompareOp) string {
	switch op {
	case cel2filter.Gt:
		return "gt"
	case cel2filter.Gte:
		return "gte"
	case cel2filter.Lt:
		return "lt"
	}
	return "lte"
}

func number(v any) bool {
	switch v.(type) {
	case int64, float64:
		return true
	}
	return false
}

func scalar(v any) bool {
	switch v.(type) {
	case string, bool, int64, float64:
		return true
	case time.Time:
		return false
	}
	return false
}

func scalars(vs []any) bool {
	for _, v := range vs {
		if !scalar(v) {
			return false
		}
	}
	return true
}
