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

// Package boltadapter contains an implementation of a BoltDB-backed onprem
// storage.
package boltadapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"intrinsic/kubernetes/data_store/firestore_shim/firestoreshimtypes"
	"intrinsic/kubernetes/data_store/revision"
	"intrinsic/kubernetes/data_store/utils/uriutils"

	log "github.com/golang/glog"
	"github.com/pborman/uuid"
	bolt "go.etcd.io/bbolt"
	"go.opencensus.io/trace"
	"google.golang.org/protobuf/proto"

	rpb "intrinsic/kubernetes/data_store/bolt_adapter/bolt_record_go_proto"
)

var (
	// ErrDataLoss is a sentinel value indicating data loss or corruption.
	ErrDataLoss = errors.New("data loss")

	// ErrInvalidURI is a sentinel value indicating that the requested path
	// is not a valid IntrinsicDB URI.
	ErrInvalidURI = errors.New("invalid URI")

	// statsLoggingInterval configures how often BoltDB stats should be logged.
	statsLoggingInterval = time.Duration(30 * time.Minute)

	// fileStatTimeout configures how long to wait for a file.Stat operation on
	// the BoltDB file to complete.
	fileStatTimeout = time.Duration(1 * time.Minute)
)

// executeUntilCancelled runs the specified functor immediately and after
// every interval. It returns a cancellation function, which must be called
// and will stop the loop.
func executeUntilCancelled(interval time.Duration, f func()) func() {
	cancel := make(chan bool)
	go func() {
		t := time.NewTicker(interval)
		f()
		for {
			select {
			case <-t.C:
				f()
			case <-cancel:
				return
			}
		}
	}()
	return func() { cancel <- true }
}

// BoltForceOpen attempts to open a BoltDB file with given parameters. If it
// fails because the file is corrupted, it deletes the file and creates it anew.
// It does not backup the file since we have a backup on Firestore anyway.
func BoltForceOpen(path string, mode os.FileMode, options *bolt.Options) (*bolt.DB, error) {
	bdb, err := bolt.Open(path, mode, options)
	if errors.Is(err, bolt.ErrInvalid) || errors.Is(err, bolt.ErrVersionMismatch) || errors.Is(err, bolt.ErrChecksum) {
		log.Warningf("Invalid BoltDB file %q: %v.", path, err)
		log.Warning("Attempting file removal.")
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, fmt.Errorf("cannot remove invalid BoltDB file %q: %w", path, removeErr)
		}
		log.Warning("File removed, initializing new BoltDB file.")
		// Assumption is that there will be no error after removing the file.
		return bolt.Open(path, mode, options)
	}
	if err != nil {
		log.Fatalf("Encountered unrecoverable error when opening BoltDB file %q, not attempting file deletion", path)
		return nil, fmt.Errorf("invalid BoltDB file %q: %w", path, err)
	}
	return bdb, nil
}

// BoltAdapter implements the onprem volume of IntrinsicDB using BoltDB.
type BoltAdapter struct {
	db      *bolt.DB
	dbL     sync.Mutex
	cleanup func()
}

// New creates a new [BoltAdapter].
func New(db *bolt.DB) *BoltAdapter {
	cancel := executeUntilCancelled(statsLoggingInterval, func() {
		ctx := context.Background()
		ctx, cancel := context.WithTimeout(ctx, fileStatTimeout)
		defer cancel()

		stat, err := os.Stat(db.Path())
		if err != nil {
			log.WarningContextf(ctx, "Could not stat %q: %v", db.Path(), err)
		} else {
			log.InfoContextf(ctx, "BoltDB file size: %d bytes", stat.Size())
		}
		log.InfoContextf(ctx, "BoltDB stats: %+v", db.Stats())
	})
	return &BoltAdapter{
		db:      db,
		cleanup: cancel,
	}
}

// GetDocumentAsType retrieves a document by a given URI and
// unmarshals it to a given proto format. Returns the document revision if the
// document exists. If the document does not exist, return an empty revision.
// If there was an error accessing it, return the error (revision is undefined).
func (s *BoltAdapter) GetDocumentAsType(ctx context.Context, uri string, p proto.Message) (revision.Revision, error) {
	_, span := trace.StartSpan(ctx, "BoltAdapter.GetDocumentAsType")
	defer span.End()

	if span.SpanContext().IsSampled() {
		log.InfoContextf(ctx, "Trace ID: %s", span.SpanContext().TraceID)
	}

	var recordAsBytes []byte

	// This is for behavior compatibility with the Firestore implementation.
	paths, err := uriutils.ParseURIForFirestore(uri)
	if err != nil {
		return revision.Empty, fmt.Errorf("%v: %w", err, ErrInvalidURI)
	}

	s.dbL.Lock()
	defer s.dbL.Unlock()

	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(paths[0]))
		if b == nil {
			return nil
		}
		for _, p := range paths[1 : len(paths)-1] {
			b = b.Bucket([]byte(p))
			if b == nil {
				return nil
			}
		}
		v := b.Get([]byte(paths[len(paths)-1]))
		// Returned slice is only valid inside of the transaction, so copy it out.
		recordAsBytes = append([]byte(nil), v...)
		return nil
	}); err != nil {
		return revision.Empty, fmt.Errorf("cannot get document %q: %w", uri, err)
	}
	if recordAsBytes == nil { // Not found, or is a bucket.
		return revision.Empty, nil
	}

	record := &rpb.BoltRecord{}
	if err := proto.Unmarshal(recordAsBytes, record); err != nil {
		return revision.Empty, fmt.Errorf("cannot unmarshal record: %w", err)
	}
	if record.GetRevision() == "" {
		return revision.Empty, fmt.Errorf("unexpected empty revision: %w", ErrDataLoss)
	}

	switch record.Data.(type) {
	case *rpb.BoltRecord_DataAsProtoWire:
		if err := proto.Unmarshal(record.GetDataAsProtoWire(), p); err != nil {
			return revision.Empty, fmt.Errorf("unmarshal data as wire to proto: %w", err)
		}
	default:
		return revision.Empty, fmt.Errorf("unexpected serialized data type: %w", ErrDataLoss)
	}
	return revision.FromString(record.GetRevision()), nil
}

// SetDocumentAsType sets a document by a given URI.
func (s *BoltAdapter) SetDocumentAsType(ctx context.Context, uri string, token revision.Revision, overwrite bool, msg proto.Message) error {
	_, span := trace.StartSpan(ctx, "BoltAdapter.SetDocumentAsType")
	defer span.End()

	if span.SpanContext().IsSampled() {
		log.InfoContextf(ctx, "Trace ID: %s", span.SpanContext().TraceID)
	}

	// This is for behavior compatibility with the Firestore implementation.
	paths, err := uriutils.ParseURIForFirestore(uri)
	if err != nil {
		return fmt.Errorf("%v: %w", err, ErrInvalidURI)
	}

	v, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("cannot marshal message to proto wire: %w", err)
	}
	record := &rpb.BoltRecord{
		Revision: uuid.New(),
		Data:     &rpb.BoltRecord_DataAsProtoWire{DataAsProtoWire: v},
	}
	recordBytes, err := proto.Marshal(record)
	if err != nil {
		return fmt.Errorf("cannot marshal record to bytes: %w", err)
	}

	s.dbL.Lock()
	defer s.dbL.Unlock()
	if err := s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(paths[0]))
		if err != nil {
			return fmt.Errorf("cannot create bucket: %w", err)
		}
		for _, p := range paths[1 : len(paths)-1] {
			b, err = b.CreateBucketIfNotExists([]byte(p))
			if err != nil {
				return fmt.Errorf("cannot create bucket: %w", err)
			}
		}

		oldRecord := &rpb.BoltRecord{}
		v := b.Get([]byte(paths[len(paths)-1]))
		if v != nil {
			if err := proto.Unmarshal(v, oldRecord); err != nil {
				return fmt.Errorf("cannot unmarshal record: %w", err)
			}
			// We need only the revision token, so don't deserialize the data.
			oldRev := oldRecord.GetRevision()

			if token.IsEmpty() && !overwrite {
				return revision.ErrDocumentExistsNoTokenSpecified
			}
			if revision.FromString(oldRev) != token && !overwrite {
				return revision.ErrTokenIsObsolete
			}
		} else {
			// TODO(mikhailpak): Do we need to handle those cases when the
			// URI is the name of a nested bucket? Probably not, since we have a fixed
			// convention for path nesting.
			if !token.IsEmpty() && !overwrite {
				return revision.ErrCannotUpdateNonExistingDocument
			}
		}
		if err := b.Put([]byte(paths[len(paths)-1]), recordBytes); err != nil {
			return fmt.Errorf("cannot write to %q: %w", uri, err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("cannot set document %q: %w", uri, err)
	}
	return nil
}

// DeleteFirestoreDocument deletes a document by a given URI. Due to the BoltDB
// implementation, attempted deletion of a bucket and attempted deletion of a
// non-existent document will result in the same
// [revision.ErrCannotUpdateNonExistingDocument] error.
func (s *BoltAdapter) DeleteDocument(ctx context.Context, uri string, token revision.Revision) error {
	_, span := trace.StartSpan(ctx, "BoltAdapter.DeleteDocument")
	defer span.End()

	if span.SpanContext().IsSampled() {
		log.InfoContextf(ctx, "Trace ID: %s", span.SpanContext().TraceID)
	}

	if token.IsEmpty() {
		return revision.ErrRequiredTokenNotSpecified
	}

	paths, err := uriutils.ParseURIForFirestore(uri)
	if err != nil {
		return fmt.Errorf("%v: %w", err, ErrInvalidURI)
	}

	oldRecord := &rpb.BoltRecord{}

	s.dbL.Lock()
	defer s.dbL.Unlock()

	if err := s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(paths[0]))
		if b == nil {
			return revision.ErrCannotUpdateNonExistingDocument
		}
		for _, p := range paths[1 : len(paths)-1] {
			b = b.Bucket([]byte(p))
			if b == nil {
				return revision.ErrCannotUpdateNonExistingDocument
			}
		}
		key := []byte(paths[len(paths)-1])
		v := b.Get(key)
		if v == nil { // Either a bucket or non-existent.
			return revision.ErrCannotUpdateNonExistingDocument
		}
		if err := proto.Unmarshal(v, oldRecord); err != nil {
			return fmt.Errorf("cannot unmarshal record: %w", err)
		}
		// We need only the revision token, so don't deserialize the data.
		oldRev := oldRecord.GetRevision()

		if revision.FromString(oldRev) != token {
			return revision.ErrTokenIsObsolete
		}
		return b.Delete(key)
	}); err != nil {
		return fmt.Errorf("cannot delete document %q: %w", uri, err)
	}
	return nil
}

func (s *BoltAdapter) getAllValuesFromBucket(ctx context.Context, path string) (map[string][]byte, error) {
	_, span := trace.StartSpan(ctx, "BoltAdapter.getAllValuesFromBucket")
	defer span.End()

	pathElems := strings.Split(path, "/")
	kvs := make(map[string][]byte)

	s.dbL.Lock()
	defer s.dbL.Unlock()
	if err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(pathElems[0]))
		if b == nil {
			return nil
		}
		if len(pathElems) > 1 {
			for _, p := range pathElems[1:] {
				b = b.Bucket([]byte(p))
				if b == nil {
					return nil
				}
			}
		}

		b.ForEach(func(k, v []byte) error {
			kvs[string(k)] = append([]byte(nil), v...)
			return nil
		})
		return nil
	}); err != nil {
		return nil, fmt.Errorf("cannot list documents in %q: %w", path, err)
	}
	return kvs, nil
}

// ListDocumentsAsType iterates over the bucket "collectionPath", deserializing
// the contained values into a list of [firestoreshimtypes.AnnotatedProto], using
// the "msg" proto message as a schema.
func (s *BoltAdapter) ListDocumentsAsType(ctx context.Context, collectionPath string, msg proto.Message) ([]firestoreshimtypes.AnnotatedProto, error) {
	_, span := trace.StartSpan(ctx, "BoltAdapter.ListDocumentsAsType")
	defer span.End()

	if span.SpanContext().IsSampled() {
		log.InfoContextf(ctx, "Trace ID: %s", span.SpanContext().TraceID)
	}

	kvs, err := s.getAllValuesFromBucket(ctx, collectionPath)
	if err != nil {
		return nil, err
	}

	var aps []firestoreshimtypes.AnnotatedProto
	for k, v := range kvs {
		record := &rpb.BoltRecord{}
		if err := proto.Unmarshal(v, record); err != nil {
			return nil, fmt.Errorf("cannot unmarshal record: %w", err)
		}
		if record.GetRevision() == "" {
			return nil, fmt.Errorf("unexpected empty revision: %w", ErrDataLoss)
		}

		switch record.Data.(type) {
		case *rpb.BoltRecord_DataAsProtoWire:
			p := proto.Clone(msg)
			if err := proto.Unmarshal(record.GetDataAsProtoWire(), p); err != nil {
				log.WarningContextf(ctx, "Skipping proto %q, cannot unmarshal: %v", k, err)
			} else {
				aps = append(aps, firestoreshimtypes.AnnotatedProto{
					ID:       k,
					Path:     path.Join(collectionPath, k),
					Proto:    p,
					Revision: revision.FromString(record.GetRevision()),
				})
			}
		default:
			return nil, fmt.Errorf("unexpected serialized data type: %w", ErrDataLoss)
		}
	}
	return aps, nil
}

// Close safely closes the underlying BoltDB file.
func (s *BoltAdapter) Close() error {
	s.dbL.Lock()
	defer s.dbL.Unlock()

	if s.cleanup != nil {
		s.cleanup()
	}

	path := s.db.Path()

	if err := s.db.Sync(); err != nil {
		return err
	}
	log.Infof("Synced %q", path)

	log.Infof("BoltDB stats before closing: %+v", s.db.Stats())

	if err := s.db.Close(); err != nil {
		return err
	}
	log.Infof("Closed %q", path)
	return nil
}
