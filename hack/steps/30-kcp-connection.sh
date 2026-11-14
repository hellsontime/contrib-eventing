# Gets kcp credentials for the OpenFGA adapter to read AccountInfo through the
# core.platform-mesh.io export, to put account paths into events. iam-service
# gets the same. The platform-mesh-operator creates a ServiceAccount with the
# export's rights and writes a kubeconfig for it next to the ManagedProvider.
# It grants all verbs on the export's resources and has no read-only option,
# though the adapter only reads.

log "Requesting a kcp kubeconfig for core.platform-mesh.io"
existing=$(kind_kubectl -n platform-mesh-system get platformmesh platform-mesh -o json |
  jq -c '.spec.kcp.extraProviderConnections // []')
if ! jq -e 'any(.secret == "contrib-eventing-core")' <<<"$existing" >/dev/null; then
  patch=$(jq -nc --argjson list "$existing" --arg ns "$NS" '{spec: {kcp: {extraProviderConnections: ($list + [{
    path: "root:platform-mesh-system",
    apiExportNames: ["core.platform-mesh.io"],
    secret: "contrib-eventing-core",
    namespace: $ns,
    adminAuth: false
  }])}}}')
  kind_kubectl -n platform-mesh-system patch platformmesh platform-mesh --type merge -p "$patch" >/dev/null
fi
wait_for "Secret $NS/contrib-eventing-core" 180 kind_kubectl -n "$NS" get secret contrib-eventing-core
