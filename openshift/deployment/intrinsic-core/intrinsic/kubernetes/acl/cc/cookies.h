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

// Utilities for fetching cookies.
#ifndef INTRINSIC_KUBERNETES_ACL_CC_COOKIES_H_
#define INTRINSIC_KUBERNETES_ACL_CC_COOKIES_H_

#include <map>
#include <optional>
#include <string>
#include <utility>

#include "absl/container/flat_hash_map.h"
#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/string_view.h"
#include "grpcpp/server_context.h"
#include "grpcpp/support/config.h"

namespace intrinsic {
namespace acl {

inline constexpr char kCookieHeaderName[] = "cookie";

// ParseCookies parses a string of cookies into a map.
//
// The string should be a semi-colon list of key=value pairs, with surrounding
// whitespaces stripped.
//
// Example:
//   "key1=value1;key2=value2"
//
// If multiple cookies have the same key, the first one is used.
absl::StatusOr<absl::flat_hash_map<std::string, std::string>> ParseCookies(
    absl::string_view cookies);

// CookiesFromServerContext returns a map of cookies from the given server
// context's client metadata.
absl::StatusOr<absl::flat_hash_map<std::string, std::string>>
CookiesFromServerContext(const grpc::ServerContext& server_ctx);

// Cookies stores a set of cookies.
//
// There is only one value for each cookie name.
class Cookies {
 public:
  // Builder for Cookies.
  //
  // Example:
  //   Cookies cookies = Cookies::Builder()
  //                          .AddString("key1=value1;key2=value2")
  //                          .AddKeyValue("key3", "value3")
  //                          .AddFromMetadata(metadata)
  //                          .AddFromServerContext(server_ctx)
  //                          .Create();
  //
  //
  // If multiple cookies have the same key, the first one is used.
  //
  // Parsing errors are returned upon calling Create().
  class Builder {
   public:
    Builder() = default;
    ~Builder() = default;

    // Not copyable or movable.
    Builder(const Builder&) = delete;
    Builder& operator=(const Builder&) = delete;
    Builder(Builder&&) = delete;
    Builder& operator=(Builder&&) = delete;

    // Adds a key-value pair.
    Builder& AddKeyValue(absl::string_view name, absl::string_view value);

    // Adds a string of cookies.
    //
    // The string should be a semi-colon list of key=value pairs, with
    // surrounding whitespaces stripped.
    //
    // Example:
    //   "key1=value1;key2=value2"
    //
    // If multiple cookies in the same string have the same key, the first one
    // is used.
    Builder& AddString(absl::string_view cookies);

    // Adds cookies from the given metadata.
    //
    // The metadata should contain a key-value pair with key "cookie" and value
    // a string of cookies.
    //
    // Returns an error if the metadata contains multiple cookie headers.
    Builder& AddFromMetadata(
        const std::multimap<grpc::string, grpc::string>& metadata);

    // Adds cookies from the given server context.
    //
    // The server context's metadata should contain a key-value pair with key
    // "cookie" and value a string of cookies.
    //
    // Returns an error if the server context's metadata contains multiple
    // cookie headers.
    Builder& AddFromServerContext(const grpc::ServerContext& server_ctx);

    // Creates the Cookies object.
    //
    // Returns an error if the cookies cannot be parsed.
    absl::StatusOr<Cookies> Create();

   private:
    void SetOrUpdateStatus(const absl::Status& status);

    bool already_consumed_ = false;
    std::optional<absl::Status> status_;
    absl::flat_hash_map<std::string, std::string> cookies_;
  };

  Cookies() = default;
  ~Cookies() = default;

  // Copyable and movable.
  Cookies(const Cookies&) = default;
  Cookies& operator=(const Cookies&) = default;
  Cookies(Cookies&&) = default;
  Cookies& operator=(Cookies&&) = default;

  bool HasCookie(absl::string_view name) const;

  std::optional<absl::string_view> GetCookie(absl::string_view name) const;

  const absl::flat_hash_map<std::string, std::string>& AsMap() const;

  std::multimap<grpc::string, grpc::string> AsMetadata() const;

  std::string AsString() const;

 private:
  explicit Cookies(absl::flat_hash_map<std::string, std::string>&& cookies)
      : cookies_(std::move(cookies)) {}

  absl::flat_hash_map<std::string, std::string> cookies_;
};

}  // namespace acl
}  // namespace intrinsic

#endif  // INTRINSIC_KUBERNETES_ACL_CC_COOKIES_H_
