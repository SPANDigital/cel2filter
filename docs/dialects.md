# Dialects

A dialect renders a `Predicate` as the JSON object its store takes. Every dialect follows the same rule as
the translator: a node it cannot express is treated as true, so the rendering stays a superset, and the
returned `exact` flag says whether that happened. A `Not` over an inexact child is dropped, because the
negation of a superset is a subset.

Both dialects take `WithFilterable(func(field string) bool)`, the set of store keys the index can filter
on. A leaf on any other field is treated as true.

## S3 Vectors (`dialect/s3vectors`)

Renders the metadata filter document of `QueryVectors`.

| Predicate | Filter | Constraints |
|---|---|---|
| `And` | `{"$and": [...]}` (one child is unwrapped) | sum |
| `Or` | `{"$or": [...]}`; any dropped child drops the whole `Or` | sum |
| `Eq(f, v)` | `{"f": {"$eq": v}}` | 1 |
| `In(f, vs)` | `{"f": {"$in": vs}}` | len(vs) |
| `Prefix(f, s)` | `{"f": {"$startsWith": s}}` (needs ENHANCED query mode) | 1 |
| `Exists(f)` | `{"f": {"$exists": true}}` | 1 |
| `Compare(f, op, n)` | `{"f": {"$gt"\|"$gte"\|"$lt"\|"$lte": n}}` for int64 and float64 | 1 |
| `Not(Eq)`, `Not(In)` | `$ne`, `$nin`, only with `WithNegation(true)` | 1, len |
| timestamps | dropped: metadata has no timestamp type | |

`$eq` and `$in` match any element when the metadata value is a list, which is what the translator means by
a leaf on a list field. `WithMaxConstraints(n)` makes `RenderCounted` fail with `ErrConstraintLimit` when
the filter has more compared values than the index allows (100 on an ENHANCED index); `Result.Constraints`
is filled in either way so a caller with its own filter clauses can budget.

Negation is off by default: S3 Vectors does not document whether `$ne` matches a vector that lacks the key,
and the expression's semantics require it to. Turn it on once you have checked your index.

Handing it to the SDK:

```go
r, err := s3vectors.Render(pred, s3vectors.WithFilterable(filterable), s3vectors.WithMaxConstraints(100))
if err == nil && r.Filter != nil {
    in.Filter = document.NewLazyDocument(r.Filter)
}
```

## OpenSearch (`dialect/opensearch`)

Renders clauses for the `filter` array of a `bool` query.

| Predicate | Clause |
|---|---|
| `And` | `{"bool": {"filter": [...]}}` |
| `Or` | `{"bool": {"should": [...], "minimum_should_match": 1}}` |
| `Not(x)` | `{"bool": {"must_not": x}}` when `x` renders exactly |
| `Eq(f, v)` | `{"term": {"f": v}}` |
| `In(f, vs)` | `{"terms": {"f": vs}}` |
| `Prefix(f, s)` | `{"prefix": {"f": s}}` |
| `Exists(f)` | `{"exists": {"field": "f"}}` |
| `Compare(f, op, v)` | `{"range": {"f": {"gt"\|"gte"\|"lt"\|"lte": v}}}` |

The fields must be mapped as `keyword`, `boolean`, a numeric type or `date`; exclude analysed `text` fields
with `WithFilterable`. `must_not` matches documents that lack the field, which agrees with CEL.

Dates are written as RFC 3339. A `date` field holds milliseconds and the document's own timestamp is
truncated on the way in, so:

- a strict lower bound becomes a non-strict one on the floored millisecond (`gt` → `gte`), inexact;
- other bounds are floored (lower) or ceiled (upper) to a millisecond, exact when nothing was cut;
- an equality or membership test on a sub-millisecond value drops.

Map `date_nanos` fields as not filterable if you need nanosecond precision, or pre-truncate your literals.

Handing it to the client:

```go
q, exact, _ := opensearch.Render(pred, opensearch.WithFilterable(keyword))
filters := []any{scopeFilter}
if q != nil {
    filters = append(filters, q)
}
body := map[string]any{"query": map[string]any{"bool": map[string]any{"must": textQuery, "filter": filters}}}
```

## Google Drive (`dialect/googledrive`)

Renders a Drive `files.list` query, the `q` parameter
([search terms](https://developers.google.com/workspace/drive/api/guides/ref-search-terms),
[labels](https://developers.google.com/workspace/drive/api/guides/search-labels)). Predicate fields mean
nothing to Drive, so each one is mapped, and an unmapped field is treated as true:

```go
r := googledrive.Render(pred,
	googledrive.WithTerm("title", "name", googledrive.String),
	googledrive.WithTerm("updated", "modifiedTime", googledrive.Time),
	googledrive.WithValues("type", func(v any) (string, bool, bool) {
		if v == "document" {
			return "mimeType = 'application/vnd.google-apps.document'", true, true
		}
		return "", false, false
	}),
	googledrive.WithLabels("labels", resolver), // a googledrive.LabelResolver
)
call := svc.Files.List().Q("trashed = false and (" + r.Query + ")").IncludeLabels(strings.Join(r.Labels, ","))
```

- `Eq` and `In` on string terms render `=`; `Prefix` renders `contains` and is inexact (Drive matches a prefix
  of any word of `name`, and a substring of `mimeType`).
- `Compare` on time terms renders `<`, `<=`, `>`, `>=` with RFC 3339 times. Drive takes whole seconds, so a
  fractional bound widens to the enclosing second and is marked inexact.
- `Not` renders `not (…)` over an exact child.
- On the labels field, `Exists` asks the resolver's `Any` (Drive has no "has any label" term; return an OR
  over every label id, and refuse when not all ids are known), and each flatkeys entry asks `Entry(label,
  field, value)` for `'labels/ID' in labels`, `labels/ID.FIELD is not null` or a field value term.
  `Result.Labels` lists the ids used, which Drive needs in `includeLabels`.

## Writing a dialect

Implement `dialect.Dialect` (`Name`, `Render`) and call `dialect.Register` from `init`. Keep the three
rules: drop what you cannot express inside a conjunction, void a disjunction that lost a side, never render
a `Not` over something you rendered inexactly. Test it with `cel2filter.Match` as the oracle: for every
predicate and document, `Match` accepting must imply your rendering accepting, as `superset_test.go` does
with small interpreters for the two shipped dialects.
