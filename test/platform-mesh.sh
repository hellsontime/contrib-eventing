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

h upgrade --install platform-mesh-prerequisites "$CHARTS/platform-mesh-prerequisites" \
  --version "$PREREQUISITES_VERSION" --namespace platform-mesh-system --create-namespace \
  --set keycloak-operator.watchNamespaces=platform-mesh-system \
  --wait --timeout 15m
h upgrade platform-mesh-prerequisites "$CHARTS/platform-mesh-prerequisites" \
  --version "$PREREQUISITES_VERSION" --namespace platform-mesh-system --reset-values \
  --set traefik.enabled=true \
  --set traefik.service.type=NodePort \
  --set traefik.service.spec.type=NodePort \
  --set traefik.service.spec.clusterIP=10.96.188.4 \
  --set traefik.ports.websecure.exposedPort=8443 \
  --set traefik.ports.websecure.nodePort=31000 \
  --wait --timeout 15m

gateway_cluster_ip=$(k -n platform-mesh-system get svc platform-mesh-prerequisites-traefik -o jsonpath='{.spec.clusterIP}')
test -n "$gateway_cluster_ip"
# Workaround for a platform bug: the kind profile gives every platform pod host
# aliases to Traefik except the operator's own, so its Provider controller can't
# reach kcp and no ManagedProvider gets Ready.
operator_host_aliases=$(jq -cn --arg ip "$gateway_cluster_ip" --arg d "$BASE_DOMAIN" \
  '[{ip: $ip, hostnames: [$d, "kcp.api." + $d, "root.kcp." + $d]}]')
# Chart 0.61.0 knows only gateway.traefikClusterIP, 0.62.0 also gateway.clusterIP.
h upgrade --install platform-mesh-operator "$CHARTS/platform-mesh-operator" \
  --version "$OPERATOR_VERSION" --namespace platform-mesh-system \
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

k -n platform-mesh-system wait --for=condition=Ready certificate/platform-mesh-domain --timeout=10m
k -n platform-mesh-system wait --for=condition=Ready platformmeshes/platform-mesh --timeout=30m
