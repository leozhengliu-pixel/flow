{{/* Names */}}
{{- define "flow.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "flow.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "flow.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "flow.labels" -}}
helm.sh/chart: {{ include "flow.chart" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: flow
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}

{{- define "flow.selectorLabels" -}}
app.kubernetes.io/name: {{ include "flow.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "flow.egress.fullname" -}}
{{- printf "%s-egress" (include "flow.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "flow.egress.selectorLabels" -}}
app.kubernetes.io/name: flow-egress
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "flow.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "flow.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "flow.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- if .Values.image.digest -}}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}
{{- end -}}

{{- define "flow.egress.image" -}}
{{- $image := .Values.egressProxy.image -}}
{{- if $image.digest -}}
{{- printf "%s@%s" $image.repository $image.digest -}}
{{- else -}}
{{- printf "%s:%s" $image.repository (default .Chart.AppVersion $image.tag) -}}
{{- end -}}
{{- end -}}

{{- define "flow.coding.image" -}}
{{- $image := .Values.coding.image -}}
{{- if $image.digest -}}
{{- printf "%s@%s" $image.repository $image.digest -}}
{{- else -}}
{{- printf "%s:%s" $image.repository $image.tag -}}
{{- end -}}
{{- end -}}

{{/* Feature switches */}}
{{- define "flow.coding.kubernetes" -}}
{{- if and .Values.coding.enabled (eq .Values.coding.provider "kubernetes") -}}true{{- end -}}
{{- end -}}

{{- define "flow.egress.enabled" -}}
{{- if and (include "flow.coding.kubernetes" .) .Values.egressProxy.enabled -}}true{{- end -}}
{{- end -}}

{{- define "flow.egress.aclEnabled" -}}
{{- if and (include "flow.egress.enabled" .) .Values.egressProxy.aclHelper.enabled -}}true{{- end -}}
{{- end -}}

{{- define "flow.sandbox.namespace" -}}
{{- .Values.sandboxes.namespace.name -}}
{{- end -}}

{{- define "flow.multiReplica" -}}
{{- if or (gt (int .Values.replicaCount) 1) .Values.autoscaling.enabled -}}true{{- end -}}
{{- end -}}

{{/* Container ports */}}
{{- define "flow.httpPort" -}}8080{{- end -}}

{{- define "flow.runtimeContainerPort" -}}
{{- if .Values.coding.runtimePort.enabled -}}{{ .Values.coding.runtimePort.port }}{{- else -}}{{ include "flow.httpPort" . }}{{- end -}}
{{- end -}}

{{- define "flow.runtimeServicePort" -}}
{{- if .Values.coding.runtimePort.enabled -}}{{ .Values.coding.runtimePort.port }}{{- else -}}{{ .Values.service.port }}{{- end -}}
{{- end -}}

{{/* URL sandboxes and the egress helper use to reach Flow. */}}
{{- define "flow.coding.runtimeUrl" -}}
{{- if .Values.coding.runtimeUrl -}}
{{- .Values.coding.runtimeUrl | trimSuffix "/" -}}
{{- else -}}
{{- printf "http://%s.%s.svc.cluster.local:%s" (include "flow.fullname" .) .Release.Namespace (include "flow.runtimeServicePort" .) -}}
{{- end -}}
{{- end -}}

{{- define "flow.coding.proxyUrl" -}}
{{- if .Values.coding.egress.proxyUrl -}}
{{- .Values.coding.egress.proxyUrl -}}
{{- else if include "flow.egress.enabled" . -}}
{{- printf "http://%s.%s.svc.cluster.local:%d" (include "flow.egress.fullname" .) (include "flow.sandbox.namespace" .) (int .Values.egressProxy.port) -}}
{{- end -}}
{{- end -}}

{{/* RuntimeClass name; "" and "runc" mean the default runtime. */}}
{{- define "flow.runtimeClassName" -}}
{{- $name := .Values.sandboxes.runtimeClass.name | default "" -}}
{{- if ne $name "runc" -}}{{ $name }}{{- end -}}
{{- end -}}

{{- define "flow.runtimeClassHandler" -}}
{{- $rc := .Values.sandboxes.runtimeClass -}}
{{- if $rc.handler -}}
{{- $rc.handler -}}
{{- else if eq $rc.name "gvisor" -}}runsc
{{- else if eq $rc.name "kata" -}}kata
{{- else -}}{{ $rc.name }}
{{- end -}}
{{- end -}}

{{/* k=v,k2=v2 from a map, sorted. */}}
{{- define "flow.kvcsv" -}}
{{- $items := list -}}
{{- range $key, $value := . -}}
{{- $items = append $items (printf "%s=%v" $key $value) -}}
{{- end -}}
{{- join "," $items -}}
{{- end -}}

{{/* key[=value]:Effect CSV from structured tolerations. */}}
{{- define "flow.tolerationsCsv" -}}
{{- $items := list -}}
{{- range . -}}
{{- $item := .key -}}
{{- if and .value (ne (default "Equal" .operator) "Exists") -}}
{{- $item = printf "%s=%s" .key .value -}}
{{- end -}}
{{- if .effect -}}
{{- $item = printf "%s:%s" $item .effect -}}
{{- end -}}
{{- $items = append $items $item -}}
{{- end -}}
{{- join "," $items -}}
{{- end -}}

{{- define "flow.size" -}}
{{- printf "cpu=%v,memory=%v,ephemeral=%v,pids=%v" .cpu .memory .ephemeral (int64 .pids) -}}
{{- end -}}

{{/*
Shared ACL secret value: explicit, else reused from the proxy's Secret in the
cluster, else random. The result is stored back into .Values so Flow's copy
and the proxy's copy match within one render.
*/}}
{{- define "flow.egress.aclSecretValue" -}}
{{- $acl := .Values.egressProxy.aclHelper -}}
{{- if not $acl.secret -}}
{{- $existing := lookup "v1" "Secret" (include "flow.sandbox.namespace" .) (printf "%s-egress-acl" (include "flow.fullname" .)) -}}
{{- if and $existing $existing.data (index $existing.data "acl-secret") -}}
{{- $_ := set $acl "secret" (index $existing.data "acl-secret" | b64dec) -}}
{{- else -}}
{{- $_ := set $acl "secret" (randAlphaNum 48) -}}
{{- end -}}
{{- end -}}
{{- $acl.secret -}}
{{- end -}}

{{- define "flow.egress.aclSecretName" -}}
{{- if .Values.egressProxy.aclHelper.existingSecret.name -}}
{{- .Values.egressProxy.aclHelper.existingSecret.name -}}
{{- else -}}
{{- printf "%s-egress-acl" (include "flow.fullname" .) -}}
{{- end -}}
{{- end -}}

{{- define "flow.egress.aclSecretKey" -}}
{{- if .Values.egressProxy.aclHelper.existingSecret.name -}}
{{- .Values.egressProxy.aclHelper.existingSecret.key -}}
{{- else -}}acl-secret{{- end -}}
{{- end -}}

{{/* Restricted-profile container security context for sandbox-namespace Pods. */}}
{{- define "flow.restrictedContainer" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
runAsNonRoot: true
capabilities:
  drop: [ALL]
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{/* Validation: fail early on unsafe or impossible combinations. */}}
{{- define "flow.validate" -}}
{{- $v := .Values -}}
{{- if include "flow.multiReplica" . -}}
{{- if eq $v.database.driver "sqlite" -}}
{{- fail "replicaCount > 1 or autoscaling requires database.driver=postgres or mysql (SQLite is single-instance)" -}}
{{- end -}}
{{- if and (eq $v.redis.mode "disabled") (not $v.redisBundled.enabled) -}}
{{- fail "replicaCount > 1 or autoscaling requires Redis coordination (redis.mode=standalone|cluster or redisBundled.enabled)" -}}
{{- end -}}
{{- if and (eq $v.storage.driver "local") (not (has "ReadWriteMany" $v.storage.persistence.accessModes)) -}}
{{- fail "replicaCount > 1 or autoscaling requires storage.driver=s3 or a ReadWriteMany volume" -}}
{{- end -}}
{{- end -}}
{{- if and $v.postgresql.enabled $v.mysql.enabled -}}
{{- fail "enable at most one of postgresql.enabled and mysql.enabled" -}}
{{- end -}}
{{- if and $v.postgresql.enabled (ne $v.database.driver "postgres") -}}
{{- fail "postgresql.enabled requires database.driver=postgres" -}}
{{- end -}}
{{- if and $v.mysql.enabled (ne $v.database.driver "mysql") -}}
{{- fail "mysql.enabled requires database.driver=mysql" -}}
{{- end -}}
{{- if and (ne $v.database.driver "sqlite") (not $v.database.url) (not $v.database.existingSecret.name) (not $v.postgresql.enabled) (not $v.mysql.enabled) (not $v.secrets.existingSecret) -}}
{{- fail "database.url, database.existingSecret.name or a bundled database is required for postgres/mysql" -}}
{{- end -}}
{{- if $v.coding.enabled -}}
{{- if eq $v.coding.provider "disabled" -}}
{{- fail "coding.enabled requires coding.provider (kubernetes, docker or fake)" -}}
{{- end -}}
{{- if and (not $v.coding.secretKey) (not $v.secrets.connectorSecretKey) (not $v.secrets.existingSecret) (not (hasKey $v.secrets.env "FLOW_CODING_SECRET_KEY")) (not (hasKey $v.secrets.env "FLOW_CONNECTOR_SECRET_KEY")) -}}
{{- fail "coding sessions need an encryption key: coding.secretKey, secrets.connectorSecretKey or secrets.existingSecret" -}}
{{- end -}}
{{- if and (eq $v.coding.snapshot.strategy "volumesnapshot") (eq $v.coding.kubernetes.workspaceVolume "emptyDir") -}}
{{- fail "coding.snapshot.strategy=volumesnapshot needs PVC workspaces (coding.kubernetes.workspaceVolume=auto or pvc)" -}}
{{- end -}}
{{- if and (eq $v.coding.snapshot.strategy "volumesnapshot") (ne $v.coding.provider "kubernetes") -}}
{{- fail "coding.snapshot.strategy=volumesnapshot requires coding.provider=kubernetes" -}}
{{- end -}}
{{- if and (eq $v.coding.provider "kubernetes") (eq (include "flow.sandbox.namespace" .) .Release.Namespace) -}}
{{- fail "sandboxes.namespace.name must differ from the release namespace: sandboxes need their own namespace" -}}
{{- end -}}
{{- if and $v.sandboxes.runtimeClass.create (not (include "flow.runtimeClassName" .)) -}}
{{- fail "sandboxes.runtimeClass.create needs sandboxes.runtimeClass.name (gvisor, kata or a custom name)" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Bundled database passwords: explicit, else reused from the cluster, else random (stored back for this render). */}}
{{- define "flow.bundledPassword" -}}
{{- $root := index . 0 -}}
{{- $auth := index . 1 -}}
{{- $field := index . 2 -}}
{{- $secretName := index . 3 -}}
{{- if not (index $auth $field) -}}
{{- $existing := lookup "v1" "Secret" $root.Release.Namespace $secretName -}}
{{- if and $existing $existing.data (index $existing.data $field) -}}
{{- $_ := set $auth $field (index $existing.data $field | b64dec) -}}
{{- else -}}
{{- $_ := set $auth $field (randAlphaNum 32) -}}
{{- end -}}
{{- end -}}
{{- index $auth $field -}}
{{- end -}}

{{- define "flow.postgresql.password" -}}
{{- include "flow.bundledPassword" (list . .Values.postgresql.auth "password" (printf "%s-postgresql" (include "flow.fullname" .))) -}}
{{- end -}}

{{- define "flow.mysql.password" -}}
{{- include "flow.bundledPassword" (list . .Values.mysql.auth "password" (printf "%s-mysql" (include "flow.fullname" .))) -}}
{{- end -}}

{{- define "flow.mysql.rootPassword" -}}
{{- include "flow.bundledPassword" (list . .Values.mysql.auth "rootPassword" (printf "%s-mysql" (include "flow.fullname" .))) -}}
{{- end -}}
