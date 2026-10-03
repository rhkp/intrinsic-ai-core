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

package clusterservice

import (
	"context"
	"errors"
	"os/user"
	"path"
	"time"

	pb "intrinsic/config/proto/cluster_go_proto"
	"intrinsic/kubernetes/data_store/bolt_adapter/boltadapter"
	"intrinsic/kubernetes/data_store/firestore_shim/firestoreshimtypes"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"

	commonpb "intrinsic/config/proto/common_go_proto"

	log "github.com/golang/glog"

	grpcpb "intrinsic/kubernetes/data_store/proto/cluster_service_go_proto"
	svcpb "intrinsic/kubernetes/data_store/proto/cluster_service_go_proto"

	"intrinsic/util/pagination/pagetoken"

	"intrinsic/kubernetes/data_store/revision"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	clustersCollection = "Clusters"

	updateTimeField = "metadata.updateTime"

	orderByField    = updateTimeField
	paginationLimit = 40
)

type FirestoreShim interface {
	GetFirestoreDocumentAsType(ctx context.Context, uri string, p proto.Message) (revision.Revision, error)
	ListFirestoreDocumentsPaginated(ctx context.Context, msg proto.Message, params firestoreshimtypes.ListParams) ([]firestoreshimtypes.AnnotatedProto, firestoreshimtypes.PaginationBoundary, bool, error)
	ListFirestoreDocuments(ctx context.Context, msg proto.Message, params firestoreshimtypes.ListParams, usePageSize int64) ([]firestoreshimtypes.AnnotatedProto, error)
	SetFirestoreDocumentAsType(ctx context.Context, uri string, token revision.Revision, overwrite bool, msg proto.Message) error
	DeleteFirestoreDocument(ctx context.Context, uri string, token revision.Revision) error
}

var (
	errNoClusterGiven  = errors.New("no cluster given")
	errNoNameSpecified = errors.New("no name specified")

	errIdentifierIsNil  = status.Errorf(codes.FailedPrecondition, "identifier received was nil")
	errBoltAdapterIsNil = status.Errorf(codes.FailedPrecondition, "cannot use onprem volume: boltadapter is nil")

	timeNow     = time.Now     // Stubbed out for testing.
	userCurrent = user.Current // Stubbed out for testing.
)

// identifiers is a tuple of strings that uniquely identify a cluster proto.
type identifiers struct {
	name string
}

// validate checks if the identifiers tuple is valid. An identifier tuple is
// invalid as soon as one identifier is empty.
func (i *identifiers) validate() error {
	if i == nil {
		return errIdentifierIsNil
	}

	switch {
	case i.name == "":
		return errNoNameSpecified
	default:
		return nil
	}
}

// identifiable provides an interface for all incoming requests to expose the
// relevant identifier fields.
type identifiable interface {
	GetName() string
}

// extractIdentifiersFromRequest validates the provided identifiable request
// and returns an identifier.
func extractIdentifiersFromRequest(req identifiable) (*identifiers, error) {
	ids := &identifiers{
		name: req.GetName(),
	}
	if err := ids.validate(); err != nil {
		return nil, err
	}
	return ids, nil
}

// ClusterService implements the data store service for
// Cluster protos.
type ClusterService struct {
	fs  FirestoreShim
	ba  *boltadapter.BoltAdapter
	env string
}

// New constructs a ClusterService backed by a Firestore
// and/or a BoltDB adapter.
func New(fs FirestoreShim, ba *boltadapter.BoltAdapter, environment string) *ClusterService {
	return &ClusterService{
		fs:  fs,
		ba:  ba,
		env: environment,
	}
}

func (s *ClusterService) GetCluster(ctx context.Context, req *svcpb.GetClusterRequest) (*svcpb.GetClusterResponse, error) {
	ids, err := extractIdentifiersFromRequest(req)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}
	docID := convertIdentifiersToDocumentID(ids)

	uri := path.Join(clustersCollection, docID)

	p := &pb.Cluster{}
	token, err := s.fs.GetFirestoreDocumentAsType(ctx, uri, p)
	if err != nil {
		return nil, status.Errorf(codes.Unknown, "could not retrieve Cluster from a Firestore document at %q: %v", uri, err)
	}
	if token.IsEmpty() {
		return nil, status.Errorf(codes.NotFound, "no document %s", uri)
	}

	if err := onEgress(p); err != nil {
		return nil, err
	}

	// We've already checked the validity of identifiers.
	writeIdentifiersToProto(ids, p)

	res := &svcpb.GetClusterResponse{}
	res.Cluster = p
	res.RevisionToken = token.String()
	return res, nil
}

func (s *ClusterService) SetCluster(ctx context.Context, req *svcpb.SetClusterRequest) (*svcpb.SetClusterResponse, error) {
	if req.Cluster == nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request: %v", errNoClusterGiven)
	}

	p := req.GetCluster()

	if p.GetMetadata().GetIsIncomplete() {
		return nil, status.Error(codes.InvalidArgument, "invalid request: cannot save an incomplete document")
	}

	if err := onIngress(p); err != nil {
		return nil, err
	}

	if p.Metadata == nil {
		p.Metadata = &commonpb.Metadata{}
	}
	// Add this info only if the field was not provided by the client.
	if p.GetMetadata().GetLastUpdatedBy() == "" {
		if u, err := userCurrent(); err == nil {
			p.GetMetadata().LastUpdatedBy = u.Username
		} else {
			log.WarningContextf(ctx, "Failed to get user information: %v", err)
			p.GetMetadata().LastUpdatedBy = "IntrinsicDB"
		}
	}
	p.GetMetadata().UpdateTime = timestamppb.New(timeNow().UTC())

	uri, err := s.getFullFirestorePath(p)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}
	token := revision.FromString(req.GetRevisionToken())
	pOut := &pb.Cluster{}

	if err := s.fs.SetFirestoreDocumentAsType(ctx, uri, token, req.GetOverwrite(), p); err != nil {
		return nil, status.Errorf(revision.ToCanonicalCode(err), "cannot set cluster document %v: %v", *extractIdentifiersFromProto(p), err)
	}
	// Notice that we do a separate, non-atomic request to obtain the token and
	// the proto. This might potentially return a different document and a
	// newer revision token!
	// TODO(mikhailpak): Figure out how to get a revision token atomically.
	token, err = s.fs.GetFirestoreDocumentAsType(ctx, uri, pOut)
	if err != nil {
		return nil, status.Errorf(codes.Unknown, "could not retrieve the updated Firestore document: %v", err)
	}

	if err := onEgress(pOut); err != nil {
		return nil, err
	}

	res := &svcpb.SetClusterResponse{
		Cluster:       pOut,
		RevisionToken: token.String(),
	}
	return res, nil
}

func prepareListRequestForToken(req *svcpb.ListClustersRequest) *svcpb.ListClustersRequest {
	res := proto.Clone(req).(*svcpb.ListClustersRequest)
	res.PageToken = ""
	res.PageSize = 0
	return res
}

func (s *ClusterService) ListClusters(req *svcpb.ListClustersRequest, stream grpcpb.ClusterService_ListClustersServer) error {
	if req.GetPageSize() < 0 {
		return status.Errorf(codes.InvalidArgument, "negative page size is not allowed")
	}

	prefix := clustersCollection

	var aps []firestoreshimtypes.AnnotatedProto
	var nextPageToken string
	var err error

	lp := firestoreshimtypes.ListParams{
		CollectionPath: prefix,
		SelectFields:   firestoreshimtypes.AllFields,
		FilterSpec:     req.GetFilterSpec(),
		SortParams: []firestoreshimtypes.SortSpec{
			{
				OrderBy:   firestoreshimtypes.DocumentID,
				Direction: firestoreshimtypes.Ascending,
			},
		},
	}
	if req.GetPageSize() > 0 {
		lp.PageSize = req.GetPageSize()
		if lp.PageSize > paginationLimit {
			lp.PageSize = int64(paginationLimit)
		}
		pt, err := pagetoken.Deopacify(req.GetPageToken())
		if err != nil {
			return status.Errorf(codes.InvalidArgument, "cannot deopacify token: %v", err)
		}
		lp.StartAfter = firestoreshimtypes.PaginationBoundary{ID: pt.GetStartAfterId()}
		lp.SortParams[0].OrderBy = orderByField
		lp.SortParams[0].Direction = firestoreshimtypes.Descending
		var paginationBoundary firestoreshimtypes.PaginationBoundary
		var reachedEnd bool
		aps, paginationBoundary, reachedEnd, err = s.fs.ListFirestoreDocumentsPaginated(stream.Context(), &pb.Cluster{}, lp)
		if err != nil {
			return status.Errorf(codes.Unknown, "cannot list documents for application metadata: %v", err)
		}
		if !reachedEnd {
			nextPageToken, err = pagetoken.Opacify(prepareListRequestForToken(req), paginationBoundary.ID, nil)
			if err != nil {
				return status.Errorf(codes.Internal, "cannot create opaque token: %v", err)
			}
		}
	} else {
		aps, err = s.fs.ListFirestoreDocuments(stream.Context(), &pb.Cluster{}, lp, paginationLimit)
	}

	if err != nil {
		return status.Errorf(codes.Unknown, "cannot list cluster: %v", err)
	}
	for _, ap := range aps {
		p := ap.Proto.(*pb.Cluster)

		if err := onEgress(p); err != nil {
			return err
		}

		ids := extractIdentifiersFromDocumentID(ap.ID)
		if err := ids.validate(); err != nil {
			return status.Errorf(codes.DataLoss, "data corruption: loaded Cluster with document ID %q that cannot be converted into valid identifiers: %v", ap.ID, err)
		}
		writeIdentifiersToProto(ids, p)

		o := &svcpb.ListClustersResponse{}
		o.Cluster = p
		o.RevisionToken = ap.Revision.String()
		// TODO(b/271565013): Return the current page token, not the final one.
		o.NextPageToken = nextPageToken
		if err := stream.Send(o); err != nil {
			return err
		}
	}
	return nil
}

func (s *ClusterService) DeleteCluster(ctx context.Context, req *svcpb.DeleteClusterRequest) (*svcpb.DeleteClusterResponse, error) {
	ids, err := extractIdentifiersFromRequest(req)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid request: %v", err)
	}
	docID := convertIdentifiersToDocumentID(ids)

	uri := path.Join(clustersCollection, docID)

	token := revision.FromString(req.GetRevisionToken())

	if err := s.fs.DeleteFirestoreDocument(ctx, uri, token); err != nil {
		return nil, status.Errorf(revision.ToCanonicalCode(err), "could not delete Firestore document at %q: %v", uri, err)
	}

	return &svcpb.DeleteClusterResponse{}, nil
}

func (s *ClusterService) getFullFirestorePath(p *pb.Cluster) (string, error) {
	ids := extractIdentifiersFromProto(p)
	if err := ids.validate(); err != nil {
		return "", err
	}
	docID := convertIdentifiersToDocumentID(ids)

	return path.Join(clustersCollection, docID), nil
}
