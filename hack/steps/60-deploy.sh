# Installs eventing the way a platform operator would: the OCM component in a
# registry and a ManagedProvider that points at it. The platform-mesh-operator
# creates the provider workspace, puts its kubeconfig next to the
# ManagedProvider, resolves the chart with the ocm-controller and installs it
# with Flux.

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

# The ocm CLI, for hack/ocm-component.sh. Checked against config/ocm/sha256sums.
OCM_CLI_VERSION=0.54.0
OCM_CLI_IMAGE=ghcr.io/open-component-model/ocm/ocm.software/ocmcli/ocmcli-image:$OCM_CLI_VERSION@sha256:3551db7c268815e61c35ef37a37c078b35052a11011cae23fe5d2de203b7302c
ocm_dir=$SECRET_DIR/ocm-$OCM_CLI_VERSION
if [[ ! -x $ocm_dir/ocm ]]; then
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m) && arch=${arch/x86_64/amd64} && arch=${arch/aarch64/arm64}
  tarball=ocm-$OCM_CLI_VERSION-$os-$arch.tar.gz
  mkdir -p "$ocm_dir"
  curl -sSfL -o "$ocm_dir/$tarball" "https://github.com/open-component-model/ocm/releases/download/v$OCM_CLI_VERSION/$tarball"
  (cd "$ocm_dir" && grep " $tarball\$" "$ROOT/config/ocm/sha256sums" | sha256sum --quiet -c -) ||
    die "ocm CLI checksum mismatch or no checksum for $tarball"
  tar -xzf "$ocm_dir/$tarball" -C "$ocm_dir" ocm
fi

# The operator refuses to downgrade a component, so new content gets a newer
# version, and content that is deployed already keeps its version.
COMPONENT=github.com/platform-mesh/contrib-eventing
# The image hack/steps/50-image.sh pushed, as the cluster sees it.
IMAGE_PUSHED=$REGISTRY:5000/${IMAGE_TAGGED##*/}@$IMAGE_DIGEST
content_hash=$({
  cd "$ROOT" && find charts/eventing ocm hack/ocm-component.sh -type f -print0 | sort -z | xargs -0 sha256sum
  echo "$IMAGE_PUSHED"
} | sha256sum | cut -c1-12)
COMPONENT_VERSION=$(kind_kubectl -n "$NS" get managedprovider "$PROVIDER" --ignore-not-found -o json |
  jq -r '.spec.runtimeDeployments[0].ocm.version // empty')
[[ $COMPONENT_VERSION == *".h$content_hash" ]] || COMPONENT_VERSION=0.0.0-dev.$(date +%s).h$content_hash
# Each build packs the chart anew, a new digest Flux would roll out, so an
# existing version is left alone.
if curl -sf -o /dev/null -H 'Accept: application/vnd.oci.image.manifest.v1+json' \
  "http://localhost:$REGISTRY_PORT/v2/ocm/component-descriptors/$COMPONENT/manifests/$COMPONENT_VERSION"; then
  log "Component $COMPONENT_VERSION is already in $REGISTRY"
else
  log "Pushing component $COMPONENT_VERSION to $REGISTRY"
  tmp=$(mktemp -d)
  PATH=$ocm_dir:$PATH "$ROOT/hack/ocm-component.sh" "$COMPONENT_VERSION" "$IMAGE_PUSHED" "$tmp/ctf" >/dev/null
  # From the kind network: the chart's global access names the registry the
  # way the ocm-controller reaches it.
  chmod -R a+rX "$tmp"
  docker run --rm --network kind -v "$tmp/ctf:/ctf:ro" "$OCM_CLI_IMAGE" \
    transfer ctf --overwrite /ctf "http://$REGISTRY:5000/ocm" >/dev/null ||
    die "ocm transfer to $REGISTRY:5000 failed"
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

log "Applying ManagedProvider $PROVIDER (component $COMPONENT_VERSION, user claim $USER_CLAIM)"
jq -n --arg ns "$NS" --arg name "$PROVIDER" --arg version "$COMPONENT_VERSION" --arg component "$COMPONENT" \
  --arg registry "http://$REGISTRY:5000/ocm" --arg image "$IMAGE_TAGGED" --arg domain "$BASE_DOMAIN" \
  --arg sinks "$ALLOWED_SINK_HOSTS" --arg claim "$USER_CLAIM" --argjson aliases "$host_aliases" '{
  apiVersion: "providers.platform-mesh.io/v1alpha1",
  kind: "ManagedProvider",
  metadata: {name: $name, namespace: $ns},
  spec: {
    platformMeshRef: {name: "platform-mesh"},
    # uninstall.sh leaves nothing behind, so the workspace goes too.
    cleanupOnDelete: true,
    # The operator keeps the http:// of a plain-http registry.
    runtimeDeployments: [{ocm: {
      name: $name, registry: $registry, component: $component, version: $version, insecure: true,
      values: {
        # The kind node pulls nothing from the registry, so the loaded image.
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
  kind_kubectl -n "$NS" get helmrelease "$PROVIDER" -o json | jq -e --arg v "$COMPONENT_VERSION" '
    (.status.lastAttemptedRevision // "" | startswith($v + "+")) and
    any(.status.conditions[]?; .type == "Ready" and .status != "Unknown")'
}
wait_for "Flux to install chart $COMPONENT_VERSION" 300 release_settled
ready=$(kind_kubectl -n "$NS" get helmrelease "$PROVIDER" -o json | jq -r '.status.conditions[] | select(.type == "Ready")')
[[ $(jq -r .status <<<"$ready") == True ]] || die "HelmRelease $NS/$PROVIDER failed: $(jq -r .message <<<"$ready")"
kind_kubectl -n "$NS" wait --for=condition=Ready "managedprovider/$PROVIDER" --timeout=120s >/dev/null
EVENTING_WS=$(eventing_workspace)
log "Eventing is in $EVENTING_WS"
