#!/usr/bin/env bash
# Installs a released Platform Mesh into the kind cluster from test/kind.yaml,
# as the kind path of helm-charts/installation/README.md does. CI runs the e2e
# against it.
set -euo pipefail

PREREQUISITES_VERSION=${PREREQUISITES_VERSION:-0.2.2}
OPERATOR_VERSION=${OPERATOR_VERSION:-0.61.0}
PLATFORM_MESH_VERSION=${PLATFORM_MESH_VERSION:-0.6.0-build.6}
BASE_DOMAIN=${BASE_DOMAIN:-portal.localhost}
CHARTS=oci://ghcr.io/platform-mesh/helm-charts
k() { kubectl --context "${KUBE_CONTEXT:-kind-platform-mesh}" "$@"; }
h() { helm --kube-context "${KUBE_CONTEXT:-kind-platform-mesh}" "$@"; }

# Small requests for the 4 CPU CI runner, see shrink_platform below.
small_requests=(
  --set keycloak-operator.deployment.resources.requests.cpu=50m
)
for c in helm imageAutomation imageReflection kustomize notification source; do
  small_requests+=(--set "flux2.${c}Controller.resources.requests.cpu=20m")
done

h upgrade --install platform-mesh-prerequisites "$CHARTS/platform-mesh-prerequisites" \
  --version "$PREREQUISITES_VERSION" --namespace platform-mesh-system --create-namespace \
  --set keycloak-operator.watchNamespaces=platform-mesh-system \
  "${small_requests[@]}" \
  --wait --timeout 15m
h upgrade platform-mesh-prerequisites "$CHARTS/platform-mesh-prerequisites" \
  --version "$PREREQUISITES_VERSION" --namespace platform-mesh-system --reset-values \
  --set traefik.enabled=true \
  --set traefik.service.type=NodePort \
  --set traefik.service.spec.type=NodePort \
  --set traefik.service.spec.clusterIP=10.96.188.4 \
  --set traefik.ports.websecure.exposedPort=8443 \
  --set traefik.ports.websecure.nodePort=31000 \
  "${small_requests[@]}" \
  --wait --timeout 15m

gateway_cluster_ip=$(k -n platform-mesh-system get svc platform-mesh-prerequisites-traefik -o jsonpath='{.spec.clusterIP}')
test -n "$gateway_cluster_ip"
# Workaround for a platform bug: the kind profile gives every platform pod host
# aliases to Traefik except the operator's own, so its Provider controller can't
# reach kcp and no ManagedProvider gets Ready.
operator_host_aliases=$(jq -cn --arg ip "$gateway_cluster_ip" --arg d "$BASE_DOMAIN" \
  '[{ip: $ip, hostnames: [$d, "kcp.api." + $d, "root.kcp." + $d]}]')
# The kind profile of these releases requests more CPU than a 4 CPU CI runner
# has, so kcp never gets scheduled. Cut requests as platform-mesh 54f290a97
# does, which no release has yet; drop this once the pinned versions include it.
command -v yq >/dev/null || { echo "yq is required" >&2; exit 1; }
chart_dir=$(mktemp -d)
trap 'rm -rf "$chart_dir"' EXIT
h pull "$CHARTS/platform-mesh-operator" --version "$OPERATOR_VERSION" --untar --untardir "$chart_dir"
yq -i '.data["profile.yaml"] |= (from_yaml |
  .components.services.infra.values.kcp.rootShard.resources.requests = {"cpu": "100m", "memory": "512Mi"} |
  .components.services.infra.values.kcp.etcd.resources.requests.cpu = "20m" |
  .components.services["kubernetes-graphql-gateway"].values.gateway.resources.requests.cpu = "50m" |
  .components.services["kubernetes-graphql-gateway"].values.listener.resources.requests.cpu = "50m" |
  .components.services["security-operator"].values.deployment.resources.requests.cpu = "50m" |
  to_yaml)' "$chart_dir/platform-mesh-operator/files/kind-profile.yaml"
# The infra chart of this release can't scale these down. Runs next to the
# operator install, which waits for the PlatformMesh.
shrink_kcp() {
  for _ in $(seq 180); do
    k -n platform-mesh-system get cacheserver/cache-server rootshard/root >/dev/null 2>&1 && break
    sleep 5
  done
  k -n platform-mesh-system patch cacheserver cache-server --type merge -p '{"spec": {"replicas": 1}}'
  k -n platform-mesh-system patch rootshard root --type merge -p '{"spec": {"proxy": {"replicas": 1}}}'
}
shrink_kcp &
shrink=$!
# Chart 0.61.0 knows only gateway.traefikClusterIP, 0.62.0 also gateway.clusterIP.
h upgrade --install platform-mesh-operator "$chart_dir/platform-mesh-operator" \
  --namespace platform-mesh-system \
  --set ocm.enabled=true \
  --set ocm.profile=kind \
  --set gateway.className=traefik \
  --set gateway.baseDomain="$BASE_DOMAIN" \
  --set gateway.port=8443 \
  --set gateway.traefikClusterIP="$gateway_cluster_ip" \
  --set hostAliases.enabled=true \
  --set-json hostAliases.entries="$operator_host_aliases" \
  --set kcp.frontProxyPort=8443 \
  --set certificates.selfSigned.enabled=true \
  --set-string ocm.version="$PLATFORM_MESH_VERSION" \
  --wait --timeout 15m
wait "$shrink"

k -n platform-mesh-system wait --for=condition=Ready certificate/platform-mesh-domain --timeout=10m
k -n platform-mesh-system wait --for=condition=Ready platformmeshes/platform-mesh --timeout=30m
