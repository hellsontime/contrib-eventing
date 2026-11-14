# Installs eventing the way a platform operator would: the chart in a registry
# and a ManagedProvider that points at it. The platform-mesh-operator creates
# the provider workspace, puts its kubeconfig next to the ManagedProvider and
# installs the chart with Flux.

# Sinks may only point at these hosts: the demo sinks, comma separated, exact
# or *.suffix.
ALLOWED_SINK_HOSTS=${ALLOWED_SINK_HOSTS:-*.eventing-demo-sink.svc.cluster.local}
TRAEFIK_SERVICE=${TRAEFIK_SERVICE:-platform-mesh-prerequisites-traefik}

# Both adapters use the same user claim as security-operator and iam-service, so
# user ids in events are the ones the rest of the platform uses. Only email and
# sub are supported; the chart refuses anything else.
USER_CLAIM=$(kind_kubectl -n platform-mesh-system get deploy security-operator -o json |
  jq -r '[.spec.template.spec.containers[] | select(.name == "security-operator") | (.args // [])[] |
    select(startswith("--user-claim=")) | ltrimstr("--user-claim=")] | first // "email"')

# The chart version is a hash of the chart, so a chart change is a new version
# for Flux.
chart_hash=$(cd "$ROOT/charts/eventing" && find . -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -c1-12)
CHART_VERSION=0.0.0-dev-$chart_hash
# helm package gives a new digest each time, which Flux would roll out, so an
# existing version is left alone.
if curl -sf -o /dev/null -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
  "http://localhost:$REGISTRY_PORT/v2/charts/eventing/manifests/$CHART_VERSION"; then
  log "Chart eventing $CHART_VERSION is already in $REGISTRY"
else
  log "Pushing chart eventing $CHART_VERSION to $REGISTRY"
  tmp=$(mktemp -d)
  helm package "$ROOT/charts/eventing" --version "$CHART_VERSION" --app-version "${IMAGE_TAGGED##*:}" -d "$tmp" >/dev/null
  helm push "$tmp/eventing-$CHART_VERSION.tgz" "oci://localhost:$REGISTRY_PORT/charts" --plain-http >/dev/null 2>&1 ||
    die "helm push to localhost:$REGISTRY_PORT failed"
  rm -r "$tmp"
fi

# kcp hands out virtual workspace URLs with the public host names. In the kind
# install from the Platform Mesh guide they are *.localhost, so inside the
# cluster they have to point at Traefik, like the kind profile does for the
# platform's own components. Where the domain resolves to the node instead,
# Traefik doesn't listen on the exposure port and nothing is needed.
host_aliases='[]'
traefik=$(kind_kubectl -n platform-mesh-system get svc "$TRAEFIK_SERVICE" -o json 2>/dev/null || true)
if [[ -n $traefik ]] && jq -e --argjson p "$PORT" 'any(.spec.ports[]; .port == $p)' <<<"$traefik" >/dev/null; then
  host_aliases=$(jq -c --arg d "$BASE_DOMAIN" '[{ip: .spec.clusterIP, hostnames: [$d, "kcp.api." + $d, "root.kcp." + $d]}]' <<<"$traefik")
fi

log "Applying ManagedProvider $PROVIDER (image $IMAGE_TAGGED, user claim $USER_CLAIM)"
jq -n --arg ns "$NS" --arg name "$PROVIDER" --arg version "$CHART_VERSION" --arg registry "$REGISTRY:5000/charts" \
  --arg image "$IMAGE_TAGGED" --arg domain "$BASE_DOMAIN" --arg sinks "$ALLOWED_SINK_HOSTS" \
  --arg claim "$USER_CLAIM" --argjson aliases "$host_aliases" '{
  apiVersion: "providers.platform-mesh.io/v1alpha1",
  kind: "ManagedProvider",
  metadata: {name: $name, namespace: $ns},
  spec: {
    platformMeshRef: {name: "platform-mesh"},
    # uninstall.sh leaves nothing behind, so the workspace goes too.
    cleanupOnDelete: true,
    runtimeDeployments: [{flux: {
      registry: $registry, chart: "eventing", version: $version, insecure: true,
      values: {
        image: {repository: ($image | sub(":[^:]*$"; "")), tag: ($image | sub("^.*:"; "")), pullPolicy: "IfNotPresent"},
        httpRoute: {baseDomain: $domain},
        allowedSinkHosts: ($sinks | split(",")),
        userClaim: $claim,
        # From hack/steps/45-keycloak-reader.sh.
        keycloak: {readerSecret: "contrib-eventing-keycloak-reader"},
        hostAliases: $aliases
      }
    }}]
  }
}' | kind_kubectl apply -f - >/dev/null

# Flux has tried this chart version and is done with it.
release_settled() {
  kind_kubectl -n "$NS" get helmrelease "$PROVIDER" -o json | jq -e --arg v "$CHART_VERSION" '
    (.status.lastAttemptedRevision // "" | startswith($v + "+")) and
    any(.status.conditions[]?; .type == "Ready" and .status != "Unknown")'
}
wait_for "Flux to install chart $CHART_VERSION" 300 release_settled
ready=$(kind_kubectl -n "$NS" get helmrelease "$PROVIDER" -o json | jq -r '.status.conditions[] | select(.type == "Ready")')
[[ $(jq -r .status <<<"$ready") == True ]] || die "HelmRelease $NS/$PROVIDER failed: $(jq -r .message <<<"$ready")"
kind_kubectl -n "$NS" wait --for=condition=Ready "managedprovider/$PROVIDER" --timeout=120s >/dev/null
EVENTING_WS=$(eventing_workspace)
log "Eventing is in $EVENTING_WS"
