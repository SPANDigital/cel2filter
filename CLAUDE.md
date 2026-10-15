# CLAUDE.md

## Project overview

cel2filter translates type-checked CEL expressions into filter predicates for stores that are not SQL
databases (Amazon S3 Vectors, OpenSearch). Module `github.com/spandigital/cel2filter`, Go 1.26, one dependency
(`cel.dev/cel-go`). Sibling of `github.com/spandigital/cel2sql/v3`, whose conventions this repo mirrors.

The contract is partial, sound over-approximation: a node that cannot be expressed is dropped from a
conjunction or voids a disjunction, the predicate accepts a superset of what the expression accepts, and the
caller re-evaluates candidates with the compiled program unless `Result.Exact` and the dialect's `exact` are
both true. cel2sql is complete-or-error; do not import that philosophy here.

## Commands

```sh
make build          # go build ./...
make test           # race tests with coverage
make lint           # golangci-lint (v2 config in .golangci.yml)
make fmt            # go fmt + goimports
make vuln-check     # govulncheck
make ci             # fmt lint test vuln-check; run before committing
make bench          # benchmarks
make example        # go run ./examples/basic
go test -run=^$ -fuzz=FuzzConvert -fuzztime=30s   # fuzz Convert and both renderers
```

## Architecture

- `cel2filter.go`: `Convert(ast, opts...) (Result, error)`, options, the `Encoder` interface, root inference.
- `translate.go`: the walker. Negation normal form (`expr(e, negated)`); every method returns `(Predicate,
  exact)` where `(nil, true)` is a tautology and `(nil, false)` a dropped node. Field references are resolved
  from the checked AST (`ast.NavigateAST`, `NavigableExpr.Type()`); never from a hand-written field table.
  The zero-value rule lives in `leaf`: a positive leaf satisfied by the field's zero value is dropped.
- `literal.go`: literal folding, including `-(n)` and `timestamp("…")` calls.
- `predicate.go`: the IR (`And`, `Or`, `Not`, `Eq`, `In`, `Prefix`, `Exists`, `Compare`) and `String()`.
- `match.go`: `Match(pred, Fields)`, the reference semantics every dialect is tested against; `IsZero`.
- `dialect/`: `Dialect` interface (`Render(p) (map[string]any, exact, error)`) and registry.
  `dialect/s3vectors`, `dialect/opensearch`: renderers. A node a dialect cannot express is treated as true;
  a `Not` over an inexact child is dropped (negating a superset would give a subset).
- `flatkeys/`: nested two-level maps stored as flat `"L"`, `"L/F"`, `"L/F/"`, `"L/F=v"` strings; `Encoder`.
- `schema/`: declare a document variable from a `Schema` without a Go struct; absent fields read as zero
  values and `has()` is false for them, matching native structs.

## Invariants to keep

- Every change to the translator or a dialect must keep `TestSuperset` green: expression accepts ⇒ `Match`
  accepts ⇒ each rendering accepts (interpreted by the tiny evaluators in `superset_test.go`), and exact ⇒
  equivalent. Add new constructs to `propertyExprs` and new value shapes to `genDoc`.
- Encoded value comparisons are never exact (the store erases value types). Presence tests are.
- OpenSearch `date` fields hold milliseconds: strict lower bounds widen to `gte`, bounds are floored or
  ceiled outward, sub-millisecond equality drops.
- S3 Vectors: no timestamps, negation only under `WithNegation`, constraints counted per compared value.

## Code quality

Before committing: `make fmt`, `make lint`, `make test`. golangci-lint v2 is strict: doc comments on exported
symbols, `_` for unused parameters, `errors.New` for static messages, no literal repetition past goconst's
threshold, explicit numeric conversions. Tests are table-driven in external test packages with testify.

## Releases

Conventional Commits (`feat:`, `fix:`, `docs:`, `test:`, `ci:`, `chore:`). Keep `CHANGELOG.md` in
Keep-a-Changelog form with an `[Unreleased]` section; cut a release by dating it, tagging `vX.Y.Z` with
`git tag -a vX.Y.Z -m "Release vX.Y.Z"` and pushing the tag (`release.yml` publishes). A new option, dialect
or exported function is a minor bump; a change to the IR or `Dialect` interface is a major one.
