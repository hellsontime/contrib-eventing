# Gets kcp credentials for the subscription controller. We ask the
# platform-mesh-operator for a provider connection: it creates a ServiceAccount
# in root:providers:eventing with exactly the rights the APIExport needs and
# writes a kubeconfig for it into our namespace.
#
# The export must exist before this runs. The operator handles all provider
# connections in one loop and stops at the first one that fails.

log "Requesting a kcp kubeconfig from the platform-mesh-operator"
kind_kubectl create namespace "$NS" --dry-run=client -o yaml | kind_kubectl apply -f - >/dev/null

connection=$(jq -nc --arg ns "$NS" '{
  path: "root:providers:eventing",
  apiExportNames: ["eventing.platform-mesh.io"],
  secret: "contrib-eventing-kcp",
  namespace: $ns,
  adminAuth: false
}')
existing=$(kind_kubectl -n platform-mesh-system get platformmesh platform-mesh -o json |
  jq -c '.spec.kcp.extraProviderConnections // []')
if ! jq -e 'any(.secret == "contrib-eventing-kcp")' <<<"$existing" >/dev/null; then
  patch=$(jq -nc --argjson list "$existing" --argjson c "$connection" \
    '{spec: {kcp: {extraProviderConnections: ($list + [$c])}}}')
  kind_kubectl -n platform-mesh-system patch platformmesh platform-mesh --type merge -p "$patch" >/dev/null
fi
wait_for "Secret $NS/contrib-eventing-kcp" 180 kind_kubectl -n "$NS" get secret contrib-eventing-kcp
