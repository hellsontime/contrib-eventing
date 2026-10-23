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
orgs=$(kind_kubectl get ns -l eventing.platform-mesh.io/org -o jsonpath='{range .items[*]}{.metadata.labels.eventing\.platform-mesh\.io/org}{"\n"}{end}')

log "Removing Subscriptions and API bindings"
for org in $orgs; do
  # The controller is still running and removes the Triggers on the way.
  kcp "root:orgs:$org" delete subscriptions --all --timeout=60s >/dev/null 2>&1 || true
  kcp "root:orgs:$org" delete apibinding eventing.platform-mesh.io --ignore-not-found >/dev/null
done

log "Removing the components and the brokers"
kind_kubectl delete ns "$NS" eventing-demo-sink --ignore-not-found --wait=false >/dev/null
for org in $orgs; do
  kind_kubectl delete ns "pm-events-$org" --ignore-not-found --wait=false >/dev/null
done
kind_kubectl delete clusterrole contrib-eventing-subscription-controller contrib-eventing-read-namespaces --ignore-not-found >/dev/null
kind_kubectl delete clusterrolebinding contrib-eventing-subscription-controller contrib-eventing-read-namespaces \
  contrib-eventing-ingress-auth-delegator --ignore-not-found >/dev/null
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
kcp root:orgs delete apiexportpolicy eventing.platform-mesh.io --ignore-not-found >/dev/null
kcp root:providers delete workspace eventing --ignore-not-found >/dev/null

uuid=$(keycloak GET "/admin/realms/master/clients?clientId=contrib-eventing" | jq -r '.[0].id // empty')
[[ -z $uuid ]] || keycloak DELETE "/admin/realms/master/clients/$uuid" >/dev/null

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
kind_kubectl wait --for=delete ns/"$NS" ns/eventing-demo-sink ns/knative-eventing \
  $(printf 'ns/pm-events-%s ' $orgs) --timeout=300s >/dev/null 2>&1 || true
if ! $keep_org; then
  org_gone() { ! kcp root:orgs get workspace "$ORG" >/dev/null 2>&1; }
  wait_for "workspace root:orgs:$ORG to be deleted" 300 org_gone
fi
