package crud

import (
	"github.com/cnabio/cnab-go/storage"
)

type CountOptions QueryOptions
type DeleteOptions QueryOptions
type ReadOptions QueryOptions

type QueryOptions struct {
	// Document is the document collection to query
	Document storage.Document
	Fields   []FieldSelector
	Labels   []LabelSelector
}

type ListOptions struct {
	QueryOptions

	// Limit the number of results, 0 indicates no limit applied.
	Limit uint

	// Skip the specified number of results, 0 indicates no skip applied.
	Skip uint

	// SortBy the specified properties.
	SortBy []string

	// ReverseSort indicates that the sort order should be reversed (descending).
	ReverseSort bool
}

type FieldSelector interface{}

type Eq struct {
	Field string
	Value interface{}
}

type LabelSelector struct {
	Key   string
	Value string
}
