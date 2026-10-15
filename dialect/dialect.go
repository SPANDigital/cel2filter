// Package dialect defines how a Predicate is rendered for one kind of store, and keeps a registry of the
// dialects the library ships. A dialect's Render never narrows the predicate: a node it cannot express is
// treated as true, and the returned exact flag says whether that happened.
package dialect

import (
	"errors"

	"github.com/spandigital/cel2filter"
)

// Name identifies a dialect.
type Name string

// The dialects the library ships.
const (
	S3Vectors   Name = "s3vectors"
	OpenSearch  Name = "opensearch"
	GoogleDrive Name = "googledrive"
)

// ErrUnsupportedFeature reports a predicate shape a dialect cannot handle at all (not a node it dropped).
var ErrUnsupportedFeature = errors.New("unsupported dialect feature")

// Dialect renders predicates for one store.
type Dialect interface {
	// Name returns the dialect's registered name.
	Name() Name
	// Render returns the store's filter for p, as the JSON object the store's API takes; nil means no
	// constraint. exact is false when a node was treated as true. err is reserved for hard limits.
	Render(p cel2filter.Predicate) (out map[string]any, exact bool, err error)
}
