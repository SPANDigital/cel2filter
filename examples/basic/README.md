# Basic example

Declares a document with the `schema` package, compiles four filters, translates each with `Convert` and
renders the result for S3 Vectors and OpenSearch, printing the predicate, both filters and the exactness
flags. The third expression shows a disjunction with a regular expression dropping to no constraint; the
fourth shows negation pushed inward.

```bash
go run ./examples/basic
```
