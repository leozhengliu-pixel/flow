{{- /*
config.file server: settings and the chart's own variables
(docs/configuration.md, "Helm").

The chart sets many FLOW_* variables itself (and the image presets a few with
ENV). Flow lets every variable win over the file, so a server: key for one of
them would never take effect. Instead, the chart writes the file's value into
its own variable:

  1. variables set by the user (config.env, coding.extraEnv, extraEnv,
     extraEnvFrom, secrets) win over everything, as before;
  2. then a config.file server: key or server.env entry;
  3. then the chart's value (config.appUrl, config.logLevel, database.*, ...);
  4. then Flow's built-in default.

A few variables follow from the Pod the chart builds (its port, paths,
volumes, database and storage drivers, Redis mode); the file may not set
those while the chart sets them, and rendering fails naming the chart value
to use instead.
*/ -}}

{{- /* Typed server: keys whose variable the chart may set: variable -> key under server. */ -}}
{{- define "flow.config.fileServerKeys" -}}
FLOW_APP_URL: appUrl
FLOW_LOG_LEVEL: log.level
FLOW_LOG_FORMAT: log.format
FLOW_ASSET_SHARED_KEEP_RELEASES: assets.sharedKeepReleases
FLOW_ASSET_FALLBACK_URL: assets.fallbackUrls
FLOW_DATABASE_PATH: database.path
FLOW_DATABASE_MAX_OPEN_CONNS: database.maxOpenConns
FLOW_DATABASE_MAX_IDLE_CONNS: database.maxIdleConns
FLOW_DATABASE_CONN_MAX_LIFETIME: database.connMaxLifetime
FLOW_REDIS_ADDRS: redis.addrs
FLOW_REDIS_TLS: redis.tls
FLOW_REDIS_PREFIX: redis.prefix
FLOW_S3_BUCKET: storage.s3.bucket
FLOW_S3_REGION: storage.s3.region
FLOW_S3_ENDPOINT: storage.s3.endpoint
FLOW_S3_PATH_STYLE: storage.s3.pathStyle
FLOW_S3_PREFIX: storage.s3.prefix
FLOW_TRUSTED_PRIVATE_ENDPOINTS: outbound.trustedPrivateEndpoints
FLOW_TRUSTED_PRIVATE_NETWORKS: outbound.trustedPrivateNetworks
FLOW_TRUST_PROXY_HEADERS: proxy.trustHeaders
FLOW_TRUSTED_PROXIES: proxy.trustedProxies
{{- end -}}

{{- /* Variables that follow from the Pod: variable -> key under server and what to do instead. */ -}}
{{- define "flow.config.chartOwnedKeys" -}}
FLOW_HTTP_ADDR: {key: httpAddr, use: "the chart serves on port 8080; leave it out of the file"}
FLOW_STATIC_PATH: {key: staticPath, use: "the image serves the web app from /app/web; leave it out of the file"}
FLOW_ASSET_PREVIOUS_PATH: {key: assets.previousPath, use: "the image keeps the previous release in /app/web-previous; leave it out of the file"}
FLOW_ASSET_PEERS: {key: assets.peers, use: "set assets.peerFallback.enabled instead"}
FLOW_ASSET_SHARED_PATH: {key: assets.sharedPath, use: "set assets.shared instead"}
FLOW_DATABASE_DRIVER: {key: database.driver, use: "set database.driver instead"}
FLOW_REDIS_MODE: {key: redis.mode, use: "set redis.mode or redisBundled.enabled instead"}
FLOW_STORAGE_DRIVER: {key: storage.driver, use: "set storage.driver instead"}
FLOW_STORAGE_LOCAL_PATH: {key: storage.localPath, use: "uploads live on the chart's volume at /app/data/uploads; leave it out of the file"}
FLOW_TRUSTED_CA_FILE: {key: outbound.trustedCaFile, use: "set outbound.trustedCA instead"}
{{- end -}}

{{- /* flow.config.lookup (list map key...): the value at the keys as a JSON list of one, or "" when unset or null. */ -}}
{{- define "flow.config.lookup" -}}
{{- $node := first . -}}
{{- $found := true -}}
{{- range $key := rest . -}}
{{- if and $found (kindIs "map" $node) (hasKey $node $key) -}}
{{- $node = index $node $key -}}
{{- else -}}
{{- $found = false -}}
{{- end -}}
{{- end -}}
{{- if and $found (not (kindIs "invalid" $node)) -}}
{{- toJson (list $node) -}}
{{- end -}}
{{- end -}}

{{- /* flow.config.envValue: a file value as Flow turns it into its variable (lists joined with commas). */ -}}
{{- define "flow.config.envValue" -}}
{{- if kindIs "slice" . -}}
{{- $items := list -}}
{{- range . -}}
{{- $items = append $items (include "flow.config.envValue" .) -}}
{{- end -}}
{{- join "," $items -}}
{{- else if or (kindIs "float64" .) (kindIs "int64" .) (kindIs "int" .) -}}
{{- if eq (float64 .) (floor (float64 .)) -}}{{ int64 . }}{{- else -}}{{ . }}{{- end -}}
{{- else if kindIs "map" . -}}
{{- fail "config.file: a server setting the chart also sets must be a single value or a list" -}}
{{- else -}}
{{- toString . | trim -}}
{{- end -}}
{{- end -}}

{{- /*
flow.config.fileEnv (dict "root" . "env" <rendered flow.configmap.env>): the
chart variables config.file sets, as a JSON object variable -> value. Fails
for a chart-owned variable the file sets.
*/ -}}
{{- define "flow.config.fileEnv" -}}
{{- $root := .root -}}
{{- $server := $root.Values.config.file.server -}}
{{- if not (kindIs "map" $server) -}}{{- $server = dict -}}{{- end -}}
{{- $serverEnv := $server.env -}}
{{- if not (kindIs "map" $serverEnv) -}}{{- $serverEnv = dict -}}{{- end -}}
{{- $rendered := list -}}
{{- range regexFindAll "(?m)^  [A-Z][A-Z0-9_]*:" .env -1 -}}
{{- $rendered = append $rendered (trimSuffix ":" (trim .)) -}}
{{- end -}}
{{- $user := keys (default dict $root.Values.config.env) -}}
{{- if $root.Values.coding.enabled -}}
{{- $user = concat $user (keys (default dict $root.Values.coding.extraEnv)) -}}
{{- end -}}
{{- range $name, $owned := include "flow.config.chartOwnedKeys" $root | fromYaml -}}
{{- if has $name $rendered -}}
{{- if include "flow.config.lookup" (prepend (splitList "." $owned.key) $server) -}}
{{- fail (printf "config.file: server.%s sets %s, which the chart manages: %s" $owned.key $name $owned.use) -}}
{{- end -}}
{{- if hasKey $serverEnv $name -}}
{{- fail (printf "config.file: server.env.%s is managed by the chart: %s" $name $owned.use) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $result := dict -}}
{{- range $name, $key := include "flow.config.fileServerKeys" $root | fromYaml -}}
{{- $found := include "flow.config.lookup" (prepend (splitList "." $key) $server) -}}
{{- if and $found (not (has $name $user)) -}}
{{- $_ := set $result $name (include "flow.config.envValue" (first ($found | fromJsonArray))) -}}
{{- end -}}
{{- end -}}
{{- range $name, $value := $serverEnv -}}
{{- if and (not (kindIs "invalid" $value)) (not (has $name $user)) -}}
{{- $_ := set $result $name (include "flow.config.envValue" $value) -}}
{{- end -}}
{{- end -}}
{{- toJson $result -}}
{{- end -}}
