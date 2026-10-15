# Changelog

## [Unreleased]

## [0.1.0] - 2026-10-15
### Added
- **Partial, sound translation of CEL to store filters.** `Convert` walks a type-checked AST into a predicate
  tree (`And`, `Or`, `Not`, `Eq`, `In`, `Prefix`, `Exists`, `Compare`) that accepts a superset of the documents
  the expression accepts; `Result.Exact` reports when nothing was dropped. Negation is pushed inward, positive
  leaves satisfied by a field's zero value are dropped, and field types come from the checker.
- **Typed comparisons** (`<`, `<=`, `>`, `>=`) on int, double and timestamp fields, with `timestamp("…")`
  folding and tightening of fractional bounds on integer fields.
- **Encoders for map-typed fields** (`WithEncoder`) and the `flatkeys` package: a two-level map stored as
  flat `"L"`, `"L/F"`, `"L/F/"`, `"L/F=v"` strings, so a label lookup pushes down as an equality.
- **Dialects**: `dialect/s3vectors` (metadata filter JSON with constraint counting, opt-in negation) and
  `dialect/opensearch` (bool filter clauses with millisecond-aware date ranges), plus a registry.
- **`schema`**: declare a document variable from a field list without a Go struct; absent fields read as
  zero values.
- **`Match`**: the reference evaluator the renderings are tested against, usable for in-memory filtering.
- A property test over generated documents for the superset contract, a fuzz target and benchmarks.
