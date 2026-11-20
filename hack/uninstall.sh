#!/usr/bin/env bash
# Removes what install.sh added. Platform Mesh itself is left alone.
#   --keep-org      keep the demo organization
#   --keep-knative  keep Knative Eventing and NATS installed
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

if kind_kubectl -n "$NS" get secret "$PROVIDER-provider-kubeconfig" >/dev/null 2>&1; then
  ws=$(eventing_workspace)
  # A half finished install may never have bound the API.
  if [[ $(kcp "$ws" get apibinding eventing.platform-mesh.io --ignore-not-found -o jsonpath='{.status.phase}') == Bound ]]; then
    log "Removing Subscriptions"
    # The controller is still running and removes the Triggers on the way.
    kcp "$ws" delete subscriptions --all --timeout=60s >/dev/null
  else
    log "The eventing API is not bound in $ws, no Subscriptions to remove"
  fi
fi

# Undo exactly what hack/steps/45-keycloak-reader.sh changed (a stopgap until
# platform-mesh/platform-mesh#578).
log "Reverting the Keycloak stopgap"
stopgap=$(kind_kubectl -n "$NS" get configmap eventing-keycloak-reader-stopgap -o json 2>/dev/null || true)
for realm in $(jq -r '.data // {} | keys[] | select(. != "contrib-eventing-keycloak-reader")' <<<"${stopgap:-null}"); do
  mode=$(jq -r --arg r "$realm" '.data[$r]' <<<"$stopgap")
  # The realm may be gone already.
  cfg=$(keycloak GET "/admin/realms/$realm/events/config" 2>/dev/null) || continue
  case $mode in
  # The expiration stays, it does no harm.
  enabled) new=$(jq -c '.eventsEnabled = false' <<<"$cfg") ;;
  added-login) new=$(jq -c '.enabledEventTypes -= ["LOGIN"]' <<<"$cfg") ;;
  *) continue ;;
  esac
  keycloak PUT "/admin/realms/$realm/events/config" -o /dev/null -d "$new"
done
# Deleting the client drops its role mappings. Only if the stopgap created it:
# a client that security-operator manages has to stay.
if [[ $(jq -r '.data["contrib-eventing-keycloak-reader"] // empty' <<<"${stopgap:-null}") == created ]]; then
  reader_uuid=$(keycloak GET "/admin/realms/master/clients?clientId=contrib-eventing-keycloak-reader" | jq -r '.[0].id // empty')
  [[ -z $reader_uuid ]] || keycloak DELETE "/admin/realms/master/clients/$reader_uuid" -o /dev/null
fi
# The client for hack/login.sh, if the demo org is still there.
login_uuid=$(keycloak GET "/admin/realms/$ORG/clients?clientId=contrib-eventing-login-test" 2>/dev/null | jq -r '.[0].id // empty' || true)
[[ -z $login_uuid ]] || keycloak DELETE "/admin/realms/$ORG/clients/$login_uuid" -o /dev/null

# The operator uninstalls the chart and deletes the provider workspace with the
# eventing API and the provider kubeconfig.
log "Removing ManagedProvider $PROVIDER"
kind_kubectl -n "$NS" delete managedprovider "$PROVIDER" --ignore-not-found --timeout=300s >/dev/null
# What the components created themselves. The chart keeps its NetworkPolicies,
# so they go only once the Broker is gone and nothing can reach it unchecked.
if kind_kubectl get crd brokers.eventing.knative.dev >/dev/null 2>&1; then
  # The broker's channel has a finalizer that the JetStream controller removes.
  kind_kubectl -n "$NS" delete broker default --ignore-not-found --timeout=300s >/dev/null ||
    die "Broker $NS/default is not gone, its NetworkPolicies stay"
fi
kind_kubectl -n knative-eventing delete networkpolicy -l "app.kubernetes.io/name=eventing,app.kubernetes.io/instance=$PROVIDER" \
  --ignore-not-found >/dev/null
kind_kubectl -n "$NS" delete configmap eventing-openfga-adapter-state eventing-keycloak-adapter-state --ignore-not-found >/dev/null
# The reader Secret is the stopgap's, unless security-operator manages the client.
if [[ -n $stopgap ]]; then
  kind_kubectl -n "$NS" delete secret contrib-eventing-keycloak-reader --ignore-not-found >/dev/null
  kind_kubectl -n "$NS" delete configmap eventing-keycloak-reader-stopgap --ignore-not-found >/dev/null
fi
kind_kubectl delete ns eventing-demo-sink --ignore-not-found --wait=false >/dev/null

log "Removing the core.platform-mesh.io connection"
index=$(kind_kubectl -n platform-mesh-system get platformmesh platform-mesh -o json |
  jq '(.spec.kcp.extraProviderConnections // []) | map(.secret) | index("contrib-eventing-core") // empty')
if [[ -n $index ]]; then
  kind_kubectl -n platform-mesh-system patch platformmesh platform-mesh --type json \
    -p "[{\"op\": \"remove\", \"path\": \"/spec/kcp/extraProviderConnections/$index\"}]" >/dev/null
fi
kind_kubectl -n "$NS" delete secret contrib-eventing-core --ignore-not-found >/dev/null

if ! $keep_org; then
  log "Removing demo organization $ORG"
  kcp root:orgs delete account "$ORG" --ignore-not-found --wait=false >/dev/null
fi

if ! $keep_knative; then
  log "Removing Knative Eventing"
  dir=$SECRET_DIR/knative-$KNATIVE_VERSION
  # in-memory-channel.yaml is from older installs.
  for f in mt-channel-broker.yaml eventing-jsm.yaml in-memory-channel.yaml eventing-core.yaml eventing-crds.yaml; do
    [[ -f $dir/$f ]] && kind_kubectl delete -f "$dir/$f" --ignore-not-found --wait=false >/dev/null 2>&1 || true
  done
  log "Removing NATS"
  kind_kubectl delete -f "$ROOT/config/nats/nats.yaml" --ignore-not-found --wait=false >/dev/null
fi

# Wait until everything is really gone, so install.sh can run right after.
log "Waiting for the deletions to finish"
kind_kubectl wait --for=delete ns/eventing-demo-sink ns/knative-eventing ns/nats-io --timeout=300s >/dev/null 2>&1 || true
if ! $keep_org; then
  org_gone() { ! kcp root:orgs get workspace "$ORG" >/dev/null 2>&1; }
  wait_for "workspace root:orgs:$ORG to be deleted" 300 org_gone
fi
