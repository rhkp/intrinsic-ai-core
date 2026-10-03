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

// Package revision provides types and functions used for representing a
// revision in an implementation of optimistic concurrency control.
package revision

import (
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc/codes"
)

var (
	// ErrDocumentExistsNoTokenSpecified is a sentinel error for the case when a
	// document exists, but there is no revision token provided. This error maps
	// to the `ALREADY_EXISTS` canonical error code. See go/canonical-codes.
	ErrDocumentExistsNoTokenSpecified = errors.New("document exists, but no token is specified")

	// ErrTokenIsObsolete is a sentinel error for the case when a document exists
	// and the revision token provided does not match the document's latest
	// revision. This error maps to the `ABORTED` canonical error code.
	// See go/canonical-codes.
	ErrTokenIsObsolete = errors.New("token is obsolete, the document was modified in the meantime")

	// ErrCannotUpdateNonExistingDocument is a sentinel error for the cases when a
	// document does not exist, but there is a revision token given. This can
	// happen if the document has been deleted in the meantime. This error maps to
	// the `NOT_FOUND` canonical error code. See go/canonical-codes.
	ErrCannotUpdateNonExistingDocument = errors.New("cannot update a non-existing document")

	// ErrRequiredTokenNotSpecified is a sentinel error for the case when a
	// token is required for the operation, but none was provided.  This error
	// maps to the `INVALID_ARGUMENT` canonical error code. See
	// go/canonical-codes.
	ErrRequiredTokenNotSpecified = errors.New("operation requires a token to be specified")
)

// Revision represents an equality-comparable identifier which is used for
// implementing optimistic locking.
type Revision string

// Empty represents an empty revision token.
const Empty = Revision("")

// FromTime creates a [Revision] from a time specification.
func FromTime(t time.Time) Revision {
	nanos := t.UnixNano()
	return Revision(fmt.Sprintf("%d", nanos))
}

// FromString creates a [Revision] from a string.
func FromString(s string) Revision {
	return Revision(s)
}

// String returns a string representation of a [Revision].
func (r Revision) String() string {
	return string(r)
}

// IsEmpty returns `true` if a Revision is empty, `false` otherwise.
func (r Revision) IsEmpty() bool {
	return r == Empty
}

// ToCanonicalCode compares an error against the sentinel errors defined in this
// package. If there is a match (possibly in the error chain), return a
// corresponding canonical code; return `UNKNOWN` otherwise.
// See go/canonical-codes.
func ToCanonicalCode(err error) codes.Code {
	switch {
	case errors.Is(err, ErrDocumentExistsNoTokenSpecified):
		return codes.AlreadyExists
	case errors.Is(err, ErrTokenIsObsolete):
		// The client should retry at a higher transaction level.
		return codes.Aborted
	case errors.Is(err, ErrCannotUpdateNonExistingDocument):
		return codes.NotFound
	case errors.Is(err, ErrRequiredTokenNotSpecified):
		return codes.InvalidArgument
	}
	return codes.Unknown
}
