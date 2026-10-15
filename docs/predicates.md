# Translation rules

`Convert` turns a type-checked boolean CEL expression over one root variable into a `Predicate`. This page
lists what each construct becomes and why the result is always a superset of the expression.

## The contract

For every document `d`: if the expression evaluates to `true` on `d`, then `Match(predicate, store(d))` is
`true`, where `store(d)` is the document as the store holds it (zero values omitted, list fields as lists,
encoded map fields flattened). `Result.Exact` is `true` when the predicate is also *equivalent* to the
expression on such documents.

A nil predicate means no constraint.

## Field references

A field is a chain of selects from the root (`doc.meta.owner` → key `meta.owner`, renamed through
`WithFieldMap`), optionally followed by literal keys into a map-typed field (`doc.labels.L.F` or
`doc.labels["L"]["F"]`). The field's kind is the checked type: string, bool, int, double, timestamp,
list of one of those, or a map (only with an `Encoder`). Anything else contributes no constraint.

## Rules

| CEL | Predicate | Exact |
|---|---|---|
| `a && b` | `And` of the translated sides; a side that translates to nothing is left out | all sides |
| `a \|\| b` | `Or` of the translated sides; if any side translates to nothing, no constraint | all sides |
| `!a` | negation pushed inward: `!(a \|\| b)` → `!a && !b`, `!(a && b)` → `!a \|\| !b` | |
| `doc.f == lit`, `lit == doc.f` | `Eq` | yes |
| `doc.f != lit` | `Not(Eq)` on single-valued fields; nothing on lists and encoded fields | yes |
| `lit in doc.list` | `Eq` (any element) | yes |
| `doc.f in [lits]` | `In`; an empty list is nothing | yes |
| `doc.f.startsWith(lit)` | `Prefix`; `startsWith("")` is a tautology | yes |
| `has(doc.f)` | `Exists` (set and non-empty); nothing for a field with an encoder | yes |
| `!has(doc.f)` | nothing | no |
| `doc.n > lit` etc. | `Compare`; `lit < doc.n` mirrors the operator | yes |
| `doc.flag`, `!doc.flag` | `Eq(flag, true)`, `Not(Eq(flag, true))` | yes |
| `has(doc.m.k)`, `"k" in doc.m` | `Eq(m, enc(["k"], nil))` | yes |
| `has(doc.m.k1.k2)` | `Eq(m, enc(["k1","k2"], nil))` | yes |
| `doc.m.k1.k2 == lit`, `lit in doc.m.k1.k2` | `Eq(m, enc(["k1","k2"], lit))` | no |
| `doc.m.k1.k2 in [lits]` | `In` of encoded entries | no |
| `true` | nothing | yes |
| `false` | nothing | no |
| `matches`, `contains`, `endsWith`, `size`, arithmetic, `? :`, comprehensions, optional lookups (`[?k]`, `orValue`), field-to-field comparisons, list equality, indexing | nothing | no |

## Literals

String, bool, int and double literals; the parser folds a leading minus, and `-(n)` is folded too.
`timestamp("2025-01-01T00:00:00Z")` and `timestamp(1735689600)` are folded; an unparsable string gives no
constraint. A `types.Timestamp` literal left by `cel.NewConstantFoldingOptimizer` is accepted as well.

`==` and `!=` only type-check on equal types. Ordering comparisons between int and double need
`cel.CrossTypeNumericComparisons(true)` on the environment; a fractional bound on an integer field is
tightened (`n > 2.5` → `n >= 3`, `n < 2.5` → `n <= 2`), and an integral double becomes an int.

## Negation and absent fields

A store negates a leaf as "the value differs, or the field is absent". CEL evaluates an absent field as its
zero value (`""`, `0`, `false`, the zero timestamp, an empty list), so `doc.kind != "leaf"` is true for a
document without `kind`, and `Not(Eq(kind, "leaf"))` matches it too. That agreement is why negated leaves
on single-valued fields are kept.

It breaks down in two places, which is why those cases produce no constraint:

- **List fields.** `!("x" in doc.tags)` means "no element equals x"; a store's negated equality on an array
  means "some element differs".
- **Encoded fields.** The flattened entries are a list.

The same zero-value view is why a **positive** leaf satisfied by the zero value is dropped: `doc.kind == ""`,
`doc.count <= 3` and `doc.updated < timestamp(...)` are true for documents without the field, and the store
has no key to match on. `doc.count > 3` and `doc.updated > timestamp(...)` are fine.

## Presence

`has(doc.f)` is true when the field is set and non-zero (the semantics of CEL native structs and of the
`schema` package). Stores omit empty values, so `Exists` ("the key is present") is a superset. If your store
writes empty strings or empty lists, `Exists` is still a superset, only less selective.

The same goes for collections: `has(doc.m)` on a map field with an encoder (stored as its list of entries,
absent when the map is empty) and `size(x)` compared with an integer literal on a list or encoded map field
become `Exists`. It is exact when the comparison means "not empty" (`size(x) > 0`, `>= 1`, `!= 0`) and a
superset when it asks for more (`size(x) > 2`, `size(x) == 3`). A comparison the empty collection satisfies
(`size(x) == 0`, `size(x) < 3`) has no positive form and yields no constraint.

## Exactness

`Result.Exact` is false whenever a node was dropped, a disjunction voided, the node cap exceeded, or an
encoded value comparison used (the stored entry no longer carries the value's type, so `3 == "3"` is false in
CEL but matches the entry). Combine it with the dialect's `exact` flag; only when both are true does the
rendered filter decide exactly what the expression decides, and only on documents the store holds with the
same values the expression saw.

## Limits

`WithMaxNodes` (default 64) bounds the expression tree the translator walks; a larger expression yields no
constraint. The environment's own limits (`cel.ParserExpressionSizeLimit`, `cel.ParserRecursionLimit`,
`cel.CostLimit`) still apply to compiling and evaluating the program.
