#!/usr/bin/env bash
# Removes what install.sh added. Platform Mesh itself is left alone.
#   --keep-org      keep the demo organization
#   --keep-knative  keep Knative Eventing installed
source "$(dirname "$0")/lib.sh"

keep_org=false keep_knative=false
for arg in "$@"; do
  case $arg in
  --keep-org) keep_org=true ;;
  --keep-knative) keep_knative=true ;;
  *) die "unknown option $arg" ;;
  esac
done

source "$ROOT/hack/steps/00-preflight.sh"

log "Removing Subscriptions"
# The controller is still running and removes the Triggers on the way.
kcp root:providers:eventing delete subscriptions --all --timeout=60s >/dev/null 2>&1 || true

log "Removing the components and the broker"
kind_kubectl delete ns "$NS" eventing-demo-sink --ignore-not-found --wait=false >/dev/null
kind_kubectl delete clusterrolebinding contrib-eventing-ingress-auth-delegator --ignore-not-found >/dev/null
kind_kubectl -n knative-eventing delete networkpolicy contrib-eventing-broker-ingress --ignore-not-found >/dev/null 2>&1 || true

# The provider connection has to go before the export. The platform-mesh-operator
# stops at the first connection it cannot handle.
log "Removing the eventing API from kcp"
index=$(kind_kubectl -n platform-mesh-system get platformmesh platform-mesh -o json |
  jq '(.spec.kcp.extraProviderConnections // []) | map(.secret) | index("contrib-eventing-kcp") // empty')
if [[ -n $index ]]; then
  kind_kubectl -n platform-mesh-system patch platformmesh platform-mesh --type json \
    -p "[{\"op\": \"remove\", \"path\": \"/spec/kcp/extraProviderConnections/$index\"}]" >/dev/null
fi
kcp root:providers delete workspace eventing --ignore-not-found >/dev/null

if ! $keep_org; then
  log "Removing demo organization $ORG"
  kcp root:orgs delete account "$ORG" --ignore-not-found --wait=false >/dev/null
fi

if ! $keep_knative; then
  log "Removing Knative Eventing"
  dir=$SECRET_DIR/knative-$KNATIVE_VERSION
  for f in mt-channel-broker.yaml in-memory-channel.yaml eventing-core.yaml eventing-crds.yaml; do
    [[ -f $dir/$f ]] && kind_kubectl delete -f "$dir/$f" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done
fi

# Wait until everything is really gone, so install.sh can run right after.
log "Waiting for the deletions to finish"
kind_kubectl wait --for=delete ns/"$NS" ns/eventing-demo-sink ns/knative-eventing --timeout=300s >/dev/null 2>&1 || true
if ! $keep_org; then
  org_gone() { ! kcp root:orgs get workspace "$ORG" >/dev/null 2>&1; }
  wait_for "workspace root:orgs:$ORG to be deleted" 300 org_gone
fi
