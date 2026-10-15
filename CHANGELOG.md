# Changelog

## [Unreleased]

## [0.2.0] - 2026-10-15
### Added
- **Collection presence.** `has(doc.m)` on a map field with an encoder, and `size(x)` compared with an integer
  literal on a list or encoded map field, translate to `Exists`: exactly for "not empty" (`> 0`, `>= 1`,
  `!= 0`, `!(size(x) == 0)`), as a superset for larger thresholds (`size(x) > 2`, `size(x) == 3`). Comparisons
  an empty collection satisfies (`== 0`, `< n`) still yield no constraint.
- **`dialect/googledrive`**: renders a predicate as a Google Drive `files.list` query (`q`). Fields are mapped
  by the caller: `WithTerm` for Drive string and time terms (`name`, `mimeType`, `modifiedTime`), `WithValues`
  for fields whose values stand for a query fragment, `WithLabels` with a `LabelResolver` for a flatkeys
  labels field (label, field and choice ids; "has any label" as an OR over every known id). The result lists
  the label ids used, for `includeLabels`. Fractional time bounds widen to whole seconds.
- **`flatkeys.Parse`**, the inverse of `Entry`.

### Changed
- `has(doc.m)` and `size(...)` comparisons on collections no longer yield an empty predicate (see above), so
  `Result.Exact` can now be true for them.

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
