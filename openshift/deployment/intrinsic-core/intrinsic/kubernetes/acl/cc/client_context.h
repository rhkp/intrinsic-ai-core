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

#ifndef INTRINSIC_KUBERNETES_ACL_CC_CLIENT_CONTEXT_H_
#define INTRINSIC_KUBERNETES_ACL_CC_CLIENT_CONTEXT_H_

#include <map>
#include <memory>
#include <string>

#include "absl/status/statusor.h"
#include "absl/time/time.h"
#include "grpcpp/client_context.h"
#include "grpcpp/server_context.h"
#include "grpcpp/support/string_ref.h"

namespace intrinsic {
namespace acl {

/// User Identity.

struct UserIdentifier {
  std::string identifier;
  bool verified;
};

// Abstract interface to represent and identify a user inside the Intrinsic
// stack.
//
// This identifying information should be able to be used to:
//   - Propagate user identity information through gRPC.
//   - Check ACL permissions.
class User {
 public:
  virtual ~User() = default;

  // Returns an identifier that can be used for ACL permission checks.
  virtual absl::StatusOr<UserIdentifier> GetUserIdentifier() const = 0;

  // Creates a new client context based on the data stored in this User.
  virtual std::unique_ptr<grpc::ClientContext> NewClientContext() const = 0;
};

// Creates a User object from a grpc::ServerContext.
std::unique_ptr<User> CreateGrpcMetadataUserFromServerContext(
    const ::grpc::ServerContext& server_context);

// Creates a new client context.
//
// If a server context is provided then specific auth-related metadata from the
// server context will be transferred to the client context.
// Note that the propagate_all_metadata is a temporary solution to copy all
// metadata from the server context to the client context until auth propagation
// is consolidated.
std::unique_ptr<grpc::ClientContext> NewClientContext(
    const ::grpc::ServerContext* server_context,
    bool propagate_all_metadata = false);

std::unique_ptr<grpc::ClientContext> NewClientContext(
    std::unique_ptr<grpc::ClientContext> context,
    const std::multimap<grpc::string_ref, grpc::string_ref>& metadata,
    bool propagate_all_metadata = false);

// Creates a new client context with a deadline.
//
// If a server context is provided then specific auth-related metadata from the
// server context will be transferred to the client context.
std::unique_ptr<grpc::ClientContext> NewClientContextWithDeadline(
    const ::grpc::ServerContext* server_context,
    absl::Duration deadline = absl::Seconds(60),
    bool propagate_all_metadata = false);

}  // namespace acl
}  // namespace intrinsic

#endif  // INTRINSIC_KUBERNETES_ACL_CC_CLIENT_CONTEXT_H_
