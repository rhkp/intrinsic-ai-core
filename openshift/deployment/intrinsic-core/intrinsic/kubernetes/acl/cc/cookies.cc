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

#include "intrinsic/kubernetes/acl/cc/cookies.h"

#include <cstddef>
#include <map>
#include <optional>
#include <string>
#include <utility>
#include <vector>

#include "absl/container/flat_hash_map.h"
#include "absl/log/log.h"
#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/ascii.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/str_join.h"
#include "absl/strings/str_split.h"
#include "absl/strings/string_view.h"
#include "grpcpp/server_context.h"
#include "grpcpp/support/config.h"
#include "intrinsic/util/status/status_macros.h"

namespace intrinsic {
namespace acl {

namespace {

absl::Status ParseCookiesTo(
    absl::string_view cookies,
    absl::flat_hash_map<std::string, std::string>& out) {
  if (cookies.empty()) {
    return absl::OkStatus();
  }

  for (absl::string_view cookie : absl::StrSplit(cookies, ';')) {
    absl::string_view cookie_stripped = absl::StripAsciiWhitespace(cookie);
    if (cookie_stripped.empty()) {
      continue;
    }

    std::vector<absl::string_view> parts = absl::StrSplit(cookie_stripped, '=');
    if (parts.size() != 2) {
      return absl::InvalidArgumentError(
          absl::StrCat("Input contained invalid cookie: ", cookie_stripped));
    }

    out.try_emplace(absl::StripAsciiWhitespace(parts[0]),
                    absl::StripAsciiWhitespace(parts[1]));
  }

  return absl::OkStatus();
}

// N.B. template on StringType to support both grpc::string and
// grpc::string_ref.
template <typename StringType>
absl::Status ParseCookiesTo(
    const std::multimap<StringType, StringType>& metadata,
    absl::flat_hash_map<std::string, std::string>& out) {
  size_t cookie_header_count = metadata.count(kCookieHeaderName);

  if (cookie_header_count == 0) {
    return absl::OkStatus();
  }

  if (cookie_header_count > 1) {
    return absl::InvalidArgumentError(
        absl::StrCat("Multiple cookie headers found: ", cookie_header_count));
  }

  auto it = metadata.lower_bound(kCookieHeaderName);
  return ParseCookiesTo(absl::string_view(it->second.begin(), it->second.end()),
                        out);
}

}  // namespace

absl::StatusOr<absl::flat_hash_map<std::string, std::string>> ParseCookies(
    absl::string_view cookies) {
  absl::flat_hash_map<std::string, std::string> out;
  INTR_RETURN_IF_ERROR(ParseCookiesTo(cookies, out));
  return out;
}

absl::StatusOr<absl::flat_hash_map<std::string, std::string>>
CookiesFromServerContext(const grpc::ServerContext& server_ctx) {
  absl::flat_hash_map<std::string, std::string> out;
  INTR_RETURN_IF_ERROR(ParseCookiesTo(server_ctx.client_metadata(), out));
  return out;
}

const absl::flat_hash_map<std::string, std::string>& Cookies::AsMap() const {
  return cookies_;
}

std::string Cookies::AsString() const {
  return absl::StrJoin(cookies_, ";", absl::PairFormatter("="));
}

std::multimap<grpc::string, grpc::string> Cookies::AsMetadata() const {
  std::multimap<grpc::string, grpc::string> metadata;
  metadata.insert({kCookieHeaderName, AsString()});
  return metadata;
}

bool Cookies::HasCookie(absl::string_view name) const {
  return cookies_.contains(name);
}

std::optional<absl::string_view> Cookies::GetCookie(
    absl::string_view name) const {
  auto it = cookies_.find(name);
  if (it == cookies_.end()) {
    return std::nullopt;
  }
  return it->second;
}

Cookies::Builder& Cookies::Builder::AddKeyValue(absl::string_view name,
                                                absl::string_view value) {
  cookies_.try_emplace(name, value);
  return *this;
}

void Cookies::Builder::SetOrUpdateStatus(const absl::Status& status) {
  if (status_.has_value()) {
    status_->Update(status);
  } else {
    status_ = status;
  }
}

Cookies::Builder& Cookies::Builder::AddString(absl::string_view cookies) {
  absl::Status status = ParseCookiesTo(cookies, cookies_);
  if (!status.ok()) {
    SetOrUpdateStatus(status);
    return *this;
  }

  return *this;
}

Cookies::Builder& Cookies::Builder::AddFromMetadata(
    const std::multimap<grpc::string, grpc::string>& metadata) {
  absl::Status status = ParseCookiesTo(metadata, cookies_);
  if (!status.ok()) {
    SetOrUpdateStatus(status);
    return *this;
  }
  return *this;
}

Cookies::Builder& Cookies::Builder::AddFromServerContext(
    const grpc::ServerContext& server_ctx) {
  absl::Status status = ParseCookiesTo(server_ctx.client_metadata(), cookies_);
  if (!status.ok()) {
    SetOrUpdateStatus(status);
    return *this;
  }
  return *this;
}

absl::StatusOr<Cookies> Cookies::Builder::Create() {
  if (already_consumed_) {
    return absl::FailedPreconditionError(
        "Cookies::Builder::Create() can only be called once.");
  }

  already_consumed_ = true;

  if (status_.has_value()) {
    return *status_;
  }

  return Cookies(std::move(cookies_));
}

}  // namespace acl
}  // namespace intrinsic
