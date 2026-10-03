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

// Package datastoreserver launches a gRPC server for all Intrinsic DB services.
package datastoreserver

import (
	"context"
	"fmt"
	"net"

	"intrinsic/kubernetes/data_store/bolt_adapter/boltadapter"
	"intrinsic/kubernetes/data_store/clusterservice"

	log "github.com/golang/glog"
	"github.com/pkg/errors"
	bolt "go.etcd.io/bbolt"
	"go.opencensus.io/plugin/ocgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	csgrpcpb "intrinsic/kubernetes/data_store/proto/cluster_service_go_proto"
)

// DataStoreServer contains a server for all IntrinsicDB services.
type DataStoreServer struct {
	srv *grpc.Server
	ba  *boltadapter.BoltAdapter
}

// NewClusterServiceBoltDB constructs a data store server that offers the onprem volume of the
// cluster service with the given BoltDB file. Call [DataStoreServer.Start] to launch the gRPC
// server.
func NewClusterServiceBoltDB(ctx context.Context, boltPath string) (*DataStoreServer, error) {
	boltOpts := bolt.DefaultOptions
	boltOpts.FreelistType = bolt.FreelistMapType
	bdb, err := boltadapter.BoltForceOpen(boltPath, 0o600, boltOpts)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot open BoltDB volume at %q", boltPath)
	}
	log.InfoContextf(ctx, "Opened BoltDB volume at %q", boltPath)
	boltAdapter := boltadapter.New(bdb)

	cs := clusterservice.New(nil, boltAdapter, "")

	grpcServer := grpc.NewServer(
		grpc.StatsHandler(&ocgrpc.ServerHandler{}),
		grpc.MaxRecvMsgSize(1024*1024*1024),
	)
	csgrpcpb.RegisterClusterServiceServer(grpcServer, cs)
	reflection.Register(grpcServer)
	return &DataStoreServer{srv: grpcServer, ba: boltAdapter}, nil
}

// Start launches a gRPC server for all Intrinsic DB services. The gRPC server
// will be spawned in the background, and report any errors asynchronously on
// the errout channel.
func (dss *DataStoreServer) Start(port int, errout chan<- error) {
	lis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		errout <- errors.Wrap(err, "failed to listen")
	}
	log.Infof("Started data store service at %d", port)
	go func() {
		srvErr := dss.srv.Serve(lis)
		if dss.ba != nil {
			errout <- dss.ba.Close()
		}
		errout <- srvErr
	}()
}

// Stop stops the gRPC server immediately. If active, the BoltDB file backing
// the onprem volume is closed properly.
func (dss *DataStoreServer) Stop() {
	dss.srv.Stop()
}
