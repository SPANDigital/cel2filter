# cel2filter

> Push [CEL](https://cel.dev/) expressions down into stores that are not SQL databases: Amazon S3 Vectors
> metadata filters and OpenSearch Query DSL today.

[![Go Version](https://img.shields.io/badge/Go-1.26%2B-blue)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

cel2filter is the sibling of [cel2sql](https://github.com/spandigital/cel2sql) for stores whose filter
language is not SQL: vector indexes, search engines, document stores. It reads a type-checked CEL expression
and produces a small predicate tree that a dialect renders into the store's own filter syntax.

The contract is different from cel2sql's. cel2sql translates an expression completely or returns an error,
because the database is the only evaluator. A vector or search store sits in front of a caller that still
holds the compiled CEL program, so cel2filter translates **partially and soundly**: whatever a store cannot
express is left out, and the rendered filter accepts a **superset** of the documents the expression accepts.
The caller re-evaluates the candidates with the full program, unless the result says the translation was
exact.

## Quick start

```bash
go get github.com/spandigital/cel2filter
```

```go
package main

import (
    "fmt"

    "cel.dev/cel-go/cel"
    "github.com/spandigital/cel2filter"
    "github.com/spandigital/cel2filter/dialect/opensearch"
    "github.com/spandigital/cel2filter/dialect/s3vectors"
    "github.com/spandigital/cel2filter/flatkeys"
    "github.com/spandigital/cel2filter/schema"
)

func main() {
    // 1. Declare the document the expressions are written against.
    doc := schema.NewSchema([]schema.FieldSchema{
        {Name: "kind", Type: cel.StringType},
        {Name: "tags", Type: cel.StringType, Repeated: true},
        {Name: "updated", Type: cel.TimestampType},
        {Name: "labels", Type: cel.MapType(cel.StringType, cel.MapType(cel.StringType, cel.DynType))},
    })
    env, _ := cel.NewEnv(schema.EnvOptions("doc", "Doc", doc)...)

    // 2. Compile the filter.
    ast, _ := env.Compile(`"onboarding" in doc.tags && doc.kind == "leaf" && doc.updated > timestamp("2025-01-01T00:00:00Z")`)

    // 3. Translate it.
    res, _ := cel2filter.Convert(ast, cel2filter.WithEncoder("labels", flatkeys.Encoder{}))
    fmt.Println(res.Predicate, res.Exact)
    // and(tags == "onboarding", kind == "leaf", updated > 2025-01-01T00:00:00Z) true

    // 4. Render it for each store.
    s3, _ := s3vectors.Render(res.Predicate, s3vectors.WithMaxConstraints(100))
    fmt.Println(s3.Filter, s3.Exact)
    // {"$and":[{"tags":{"$eq":"onboarding"}},{"kind":{"$eq":"leaf"}}]} false   (S3 has no timestamps)

    q, exact, _ := opensearch.Render(res.Predicate)
    fmt.Println(q, exact)
    // {"bool":{"filter":[{"term":{"tags":"onboarding"}},{"term":{"kind":"leaf"}},{"range":{"updated":{"gte":"2025-01-01T00:00:00Z"}}}]}} false

    // 5. Re-evaluate candidates with the compiled program unless res.Exact && exact.
}
```

## Sound over-approximation

Every node of the expression is translated under one rule: **the predicate must accept every document the
expression accepts**. So

- a conjunct that cannot be expressed is dropped (`a && regex(b)` becomes `a`),
- a disjunction with an inexpressible side is dropped whole (`a || regex(b)` becomes no constraint),
- negation is pushed inward first (`!(a || b)` becomes `!a && !b`, which survives `b` being inexpressible),
- a negated leaf is kept only on single-valued fields, where a store's "value differs or field absent"
  agrees with CEL's view of an absent field as its zero value,
- a positive comparison that the zero value satisfies (`doc.kind == ""`, `doc.count <= 3`) is dropped,
  because the store has no key for an absent field while the expression sees `""` or `0`.

`Result.Exact` reports when nothing was dropped. Each dialect's `Render` returns its own `exact` flag for
the nodes it could not express; a caller that gets `true` from both may skip re-evaluation.

The property test in `superset_test.go` checks the contract on hundreds of generated documents for every
construct: expression accepts ⇒ predicate accepts ⇒ each dialect's rendering accepts.

## What translates

| CEL | Predicate | Notes |
|---|---|---|
| `doc.f == "x"`, `"x" == doc.f` | `Eq` | string, bool, int, double, timestamp fields |
| `"x" in doc.list` | `Eq` | any element of a list field |
| `doc.f in ["a", "b"]` | `In` | |
| `doc.f.startsWith("a")` | `Prefix` | |
| `has(doc.f)` | `Exists` | set and non-empty |
| `has(doc.m)`, `size(doc.list) > 0`, `size(doc.m) != 0` | `Exists` | lists and encoded maps; `size(x) > 2` is a superset |
| `doc.n > 3`, `doc.t >= timestamp("…")` | `Compare` | int, double, timestamp; `3 < doc.n` mirrors |
| `doc.flag` | `Eq(flag, true)` | |
| `a && b`, `a \|\| b`, `!a`, `a != b` | `And`, `Or`, `Not` | negation normal form |
| `has(doc.m.k)`, `"k" in doc.m`, `doc.m.k.f == "v"` | `Eq` on the encoded entry | map fields with an `Encoder` |
| `matches`, `contains`, arithmetic, ternaries, comprehensions, `size(x) == 0`, optional lookups, field-to-field | no constraint | `Exact` becomes false |

Field types come from the type-checker (`Convert` refuses an unchecked AST), so there is no schema to repeat.
Cross-type numeric comparisons (`doc.count > 2.5`) need `cel.CrossTypeNumericComparisons(true)` on the
environment; the translator tightens a fractional bound on an integer field to the next integer.

## Options

- `WithRoot(name)`: the variable the expression is written against (default: the only one it references).
- `WithFieldMap(map[string]string)`: rename CEL paths (`meta.owner`) to store keys.
- `WithEncoder(field, enc)`: how lookups inside a map-typed field are stored; `flatkeys.Encoder{}` is the
  shipped one (`"L"`, `"L/F"`, `"L/F=v"` entries in a keyword list).
- `WithMaxNodes(n)`: cap on the expression tree (default 64).

## Dialects

| Dialect | Package | Output | Negation | Comparisons |
|---|---|---|---|---|
| S3 Vectors | `dialect/s3vectors` | metadata filter JSON (`$and`, `$or`, `$eq`, `$in`, `$startsWith`, `$exists`, `$gt`…) | opt-in `WithNegation` (`$ne`, `$nin`) | numbers only; timestamps drop |
| OpenSearch | `dialect/opensearch` | bool filter clauses (`term`, `terms`, `prefix`, `exists`, `range`, `bool`) | `must_not` on exact leaves | numbers and dates (millisecond-widened) |
| Google Drive | `dialect/googledrive` | a `files.list` query string (`name`, `mimeType`, `modifiedTime`, label terms) | `not` on exact leaves | times (whole seconds) |

S3 Vectors and OpenSearch take `WithFilterable(func(field string) bool)` for the fields the index can filter
on; S3 Vectors also counts constraints against `WithMaxConstraints`. Google Drive maps each field explicitly
(`WithTerm`, `WithValues`, `WithLabels`). See [docs/dialects.md](docs/dialects.md).

## Match

`cel2filter.Match(pred, doc)` evaluates a predicate with the stores' semantics over a `Fields` (a
`map[string]any` works). It is the reference the dialect renderings are tested against and is useful for
in-memory filtering and for checking a store's behaviour.

## Documentation

- [docs/predicates.md](docs/predicates.md): every translation rule, negation, presence and zero values.
- [docs/dialects.md](docs/dialects.md): per-dialect rendering and how to hand the output to the client SDKs.
- [examples/basic](examples/basic): the quick start as a runnable program.

## Requirements

Go 1.26 or later. The only dependency is `cel.dev/cel-go`.

## License

Apache 2.0. See [LICENSE](LICENSE).

## Related projects

- [cel2sql](https://github.com/spandigital/cel2sql): CEL to SQL for PostgreSQL, MySQL, SQLite, DuckDB, BigQuery, Spark and the OpenSearch SQL plugin.
- [cel-go](https://github.com/google/cel-go): the Common Expression Language in Go.
