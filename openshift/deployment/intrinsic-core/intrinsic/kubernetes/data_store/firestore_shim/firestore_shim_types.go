// Copyright 2026 Intrinsic Innovation LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package firestoreshimtypes contains data type definitions common to all Firestore shims.
package firestoreshimtypes

import (
	"errors"
	"fmt"

	"intrinsic/kubernetes/data_store/revision"

	"cloud.google.com/go/firestore"
	"google.golang.org/protobuf/proto"

	fspb "intrinsic/kubernetes/data_store/proto/filter_spec_go_proto"
)

const (
	// Ascending order of results (low to high).
	Ascending = iota + 10 // Make sure it's not zero.
	// Descending order of results (high to low).
	Descending
)

var (
	// AllFields is a sentinel value to be used when listing Firestore documents if the whole document
	// is to be downloaded.
	AllFields []firestore.FieldPath = nil

	// ErrEvenNumIDsOrEmptyID is a sentinel error which indicates that the provided collection path is
	// invalid.
	ErrEvenNumIDsOrEmptyID = errors.New("path contains an even number of IDs or an empty ID")
	// ErrCannotConvertPaginationBoundaryToString is a sentinel error which is returned when the
	// requested pagination boundary field cannot be converted to a string.
	ErrCannotConvertPaginationBoundaryToString = errors.New("cannot convert pagination boundary to string")
	// ErrInvalidSortParams is a sentinel error value which indicates that the provided sort
	// parameters (a list of [SortSpec]) are invalid.
	ErrInvalidSortParams = errors.New("invalid sort parameters")

	// DocumentID is a convenience re-export of [firestore.DocumentID].
	DocumentID = firestore.DocumentID
)

// AnnotatedProto is a named tuple of a database document ID, corresponding proto message, and its
// revision.
type AnnotatedProto struct {
	ID       string
	Path     string
	Proto    proto.Message
	Revision revision.Revision
	Boundary PaginationBoundary
}

// SortSpec specifies how to sort the query results.
type SortSpec struct {
	// OrderBy specifies the field by which the list results should be ordered. If
	// unset, the order is deterministic, but unspecified.
	OrderBy string
	// Direction specifies the sort direction. Allowed values: [Ascending]
	// and [Descending]. Default is [Ascending].
	Direction int32
}

// CheckSortSpecs returns an [ErrInvalidSortParams] error if the input value is invalid.
func CheckSortSpecs(specs []SortSpec) error {
	if len(specs) == 0 {
		return fmt.Errorf("no SortParams specified: %w", ErrInvalidSortParams)
	}
	seen := make(map[string]struct{}, len(specs))
	for idx, s := range specs {
		if s.Direction != Ascending && s.Direction != Descending {
			return fmt.Errorf("sort spec at index %d: invalid direction %d: %w", idx, s.Direction, ErrInvalidSortParams)
		}
		if s.OrderBy == "" {
			return fmt.Errorf("sort spec at index %d: OrderBy cannot be empty: %w", idx, ErrInvalidSortParams)
		}
		// Check for duplicates.
		if _, ok := seen[s.OrderBy]; ok {
			return fmt.Errorf("sort spec at index %d: duplicate OrderBy %q: %w", idx, s.OrderBy, ErrInvalidSortParams)
		}
		seen[s.OrderBy] = struct{}{}
	}
	return nil
}

// PaginationBoundary represents either a list of field values that are passed to
// [firestore.StartAfter], or the document ID that is used to get a snapshot that is passed to
// [firestore.StartAfter].
type PaginationBoundary struct {
	ID     string
	Values []any
}

// ListParams is a container for list query options.
type ListParams struct {
	// CollectionPath is the slash-separated path of the collection to list.
	CollectionPath string
	// FilterSpec specifies which Firestore documents should be included in the
	// list results.
	FilterSpec *fspb.FilterSpec
	// SelectFields specifies the fields of the Firestore document to download
	// (field mask).
	SelectFields []firestore.FieldPath
	// PageSize can be set only if calling [ListFirestoreDocumentsPaginated].
	PageSize int64
	// SortParams defines how to sort (and paginate) the results, allowing users to specify several
	// fields to sort by. This field MUST be specified and the list MUST have at least one [SortSpec].
	SortParams []SortSpec
	// StartAfter can be set only if calling [ListFirestoreDocumentsPaginated].
	// This paginated list request will order all elements according to the [ListParams.SortParams].
	// Now there are three cases possible:
	//
	//   1. If [PaginationBoundary.ID] is non-empty: Start after this element.
	//   2. If [PaginationBoundary.ID] is empty and [PaginationBoundary.Values] are non-nil: Return
	//      elements that are after the element whose [SortSpec.OrderBy]'s field values are *equal* to
	//      [PaginationBoundary.Values]. Normally, you are supposed to set
	//      [ListParams.ExtractPaginationBoundaryValues] to true and then use
	//      ListFirestoreDocumentsPaginated's [PaginationBoundary] return value to populate this field.
	//	    Its length *must* be the same as [ListParams.SortParams]'s length.
	//   3. If [PaginationBoundary.ID] and [PaginationBoundary.Values] are both empty, then it has
	//      the special semantics of starting from the beginning of the ordered collection.
	StartAfter PaginationBoundary
	// If true, the Firestore shim will populate the [PaginationBoundary] with the field values and
	// the document ID. Otherwise, only populate the document ID.
	ExtractPaginationBoundaryValues bool
}

// ToFirestoreDirection extracts the sorting direction from [SortSpec].
func (p SortSpec) ToFirestoreDirection() (firestore.Direction, error) {
	switch p.Direction {
	case Descending:
		return firestore.Desc, nil
	case Ascending:
		fallthrough
	case 0:
		return firestore.Asc, nil
	default:
		return 0, fmt.Errorf("invalid direction: %d", p.Direction)
	}
}

// MakePaginationBoundaryFromStrings converts a list of strings to a [PaginationBoundary], with
// only the [PaginationBoundary.Values] filled. The library developer needs to add the
// [PaginationBoundary.ID] themselves if they wish to.
func MakePaginationBoundaryFromStrings(ss []string) PaginationBoundary {
	var b PaginationBoundary
	for _, s := range ss {
		b.Values = append(b.Values, s)
	}
	return b
}

// ToStrings converts [PaginationBoundary.Values] to a list of strings. If one of the
// [PaginationBoundary.Values] elements cannot be converted to a string, return the
// [ErrCannotConvertPaginationBoundaryToString] error. [PaginationBoundary.ID] is ignored.
func (b PaginationBoundary) ToStrings() ([]string, error) {
	var ss []string
	for _, a := range b.Values {
		s, ok := a.(string)
		if !ok {
			return nil, ErrCannotConvertPaginationBoundaryToString
		}
		ss = append(ss, s)
	}
	return ss, nil
}
