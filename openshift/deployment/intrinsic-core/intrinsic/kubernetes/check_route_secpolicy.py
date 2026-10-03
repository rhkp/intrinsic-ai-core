# Copyright 2026 Intrinsic Innovation LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

#!/usr/bin/env python3

# Copyright 2026 Intrinsic Innovation LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Linter to verify HTTPRoute and GRPCRoute have matching SecurityPolicy targetRefs."""

import sys

import yaml

_SUPPORTED_ROUTE_KINDS = ("HTTPRoute", "GRPCRoute")
_POLICY_KINDS = ("SecurityPolicy",)


def check_route_secpolicies(yaml_file_path: str) -> bool:
  """Verify every HTTPRoute and GRPCRoute in yaml_file_path has a matching SecurityPolicy targetRef."""
  with open(yaml_file_path, "r", encoding="utf-8") as f:
    try:
      docs = list(yaml.safe_load_all(f))
    except yaml.YAMLError as e:
      print(
          f"ERROR: Failed to parse YAML file {yaml_file_path}: {e}",
          file=sys.stderr,
      )
      return False

  routes: list[tuple[str, str]] = []
  targeted_routes: set[tuple[str, str]] = set()

  for doc in docs:
    if not isinstance(doc, dict):
      continue

    kind = doc.get("kind")
    metadata = doc.get("metadata") or {}
    name = metadata.get("name")

    if kind in _SUPPORTED_ROUTE_KINDS and name:
      routes.append((kind, name))

    if kind in _POLICY_KINDS:
      spec = doc.get("spec") or {}
      target_refs = spec.get("targetRefs") or []
      if not isinstance(target_refs, list):
        target_refs = [target_refs]
      single_target_ref = spec.get("targetRef")
      if single_target_ref:
        target_refs.append(single_target_ref)

      for ref in target_refs:
        if not isinstance(ref, dict):
          continue
        ref_kind = ref.get("kind")
        ref_name = ref.get("name")
        ref_group = ref.get("group")
        if ref_kind in _SUPPORTED_ROUTE_KINDS and ref_name:
          if not ref_group or ref_group == "gateway.networking.k8s.io":
            targeted_routes.add((ref_kind, ref_name))

  missing_policies = [r for r in routes if r not in targeted_routes]

  if missing_policies:
    print(
        "\nERROR: Found Route resources missing matching SecurityPolicy"
        f" targetRefs in {yaml_file_path}:",
        file=sys.stderr,
    )
    for route_kind, route_name in missing_policies:
      print(
          f"  - {route_kind} '{route_name}' has no SecurityPolicy with"
          f" targetRef matching kind={route_kind}, name={route_name}",
          file=sys.stderr,
      )
    return False

  if routes:
    print(
        f"Route SecurityPolicy check passed: verified {len(routes)}"
        f" route(s) ({', '.join(f'{kind}/{name}' for kind, name in routes)})."
    )
  return True


def main() -> None:
  """Main entry point for checking route security policies across input YAML files."""
  if len(sys.argv) < 2:
    print(f"Usage: {sys.argv[0]} <yaml_file>", file=sys.stderr)
    sys.exit(1)

  success = True
  for yaml_file in sys.argv[1:]:
    if not check_route_secpolicies(yaml_file):
      success = False

  if not success:
    sys.exit(1)


if __name__ == "__main__":
  main()
