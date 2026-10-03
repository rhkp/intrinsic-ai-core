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

{{/*
Generates SecurityPolicy for authproxy2 (Envoy Gateway gRPC external authorization service).

Parameters:
  - route: (required) Target HTTPRoute/GRPCRoute resource name.
  - context: (required) Root Helm template context ($).
  - kind: (optional, default "HTTPRoute") Target route kind ("HTTPRoute" or "GRPCRoute").
  - scopes: (optional) Auth scopes to pass via contextExtensions (e.g. "internal", "clusterheader", "tokenvendor,tvrobot", "tokenvendor,tvhuman", "serviceauth").
  - endpoint: (optional) Auth endpoint ("authorizeCluster", "checkAuthentication").
  - app: (optional, default .route) Label app: <app>.
  - namespace: (optional, default .context.Release.Namespace) Route/caller namespace.
  - authService: (optional, default "authproxy2") Target auth service name.
  - authNamespace: (optional, default "app-authproxy2") Target auth service namespace.
  - authPort: (optional, default 8081) Target auth service port.

Usage:
  {{ include "auth.proxy2" (dict "route" "vmpoolmanager" "kind" "GRPCRoute" "context" $) }}
  {{ include "auth.proxy2" (dict "route" "vmpoolmanager-internal" "kind" "GRPCRoute" "scopes" "internal" "context" $) }}
  {{ include "auth.proxy2" (dict "route" "relay-server" "endpoint" "authorizeCluster" "scopes" "clusterheader" "context" $) }}
  {{ include "auth.proxy2" (dict "route" "relay-client" "scopes" "tokenvendor,tvrobot" "context" $) }}
*/}}
{{- define "auth.proxy2" -}}
{{- $kind := default "HTTPRoute" .kind -}}
{{- $appLabel := default .route .app -}}
{{- $callerNs := default "default" (default .context.Release.Namespace .namespace) -}}
{{- $authService := default "authproxy2" .authService -}}
{{- $authNamespace := default "app-authproxy2" .authNamespace -}}
{{- $authPort := default 8081 .authPort -}}

---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: SecurityPolicy
metadata:
  name: {{ .route }}-{{ $kind | lower }}-auth
  labels:
    app.kubernetes.io/name: {{ .context.Chart.Name }}
    app: {{ $appLabel }}
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: {{ $kind }}
      name: {{ .route }}
  extAuth:
    {{- if or .scopes .endpoint }}
    contextExtensions:
      {{- if .scopes }}
      - name: scopes
        type: Value
        value: {{ .scopes | quote }}
      {{- end }}
      {{- if .endpoint }}
      - name: endpoint
        type: Value
        value: {{ .endpoint | quote }}
      {{- end }}
    {{- end }}
    headersToExtAuth:
      - authorization
      - cookie
      - x-forwarded-access-token
      - x-server-name
    grpc:
      backendRefs:
        - name: {{ $authService }}
          namespace: {{ $authNamespace }}
          port: {{ $authPort }}
{{- include "auth._referencegrant" (dict "context" .context "callerNamespace" $callerNs "targetNamespace" $authNamespace "targetService" $authService) -}}
{{- end -}}

{{/*
Internal utility: Generates ReferenceGrant allowing SecurityPolicy to reference a cross-namespace backend Service.
Deduplicates ReferenceGrant output per Helm render run to avoid duplicate manifests.

Parameters:
  - context: (required) Root Helm template context ($).
  - callerNamespace: (required) Namespace of the SecurityPolicy.
  - targetNamespace: (required) Target namespace of the backend Service.
  - targetService: (required) Target backend Service name.
*/}}
{{- define "auth._referencegrant" -}}
{{- $callerNs := default "default" (default .context.Release.Namespace .callerNamespace) -}}
{{- if and .targetNamespace (ne .targetNamespace $callerNs) -}}
{{- /* Dedup ReferenceGrant output per Helm render run to avoid duplicates */ -}}
{{- $refGrantName := printf "refgrant-%s-%s-%s" (.context.Chart.Name | trunc 16 | trimSuffix "-") ($callerNs | trunc 18 | trimSuffix "-") (.targetService | trunc 16 | trimSuffix "-") -}}
{{- if not (hasKey (default dict .context.Values._authRefGrants) $refGrantName) -}}
{{- if not .context.Values._authRefGrants }}{{ $_ := set .context.Values "_authRefGrants" dict }}{{ end -}}
{{- $_ := set .context.Values._authRefGrants $refGrantName true }}
---
apiVersion: gateway.networking.k8s.io/v1
kind: ReferenceGrant
metadata:
  name: {{ $refGrantName }}
  namespace: {{ .targetNamespace }}
  labels:
    app.kubernetes.io/name: {{ .context.Chart.Name }}
spec:
  from:
    - group: gateway.envoyproxy.io
      kind: SecurityPolicy
      namespace: {{ $callerNs }}
  to:
    - group: ""
      kind: Service
      name: {{ .targetService }}
{{- end -}}
{{- end -}}
{{- end -}}
