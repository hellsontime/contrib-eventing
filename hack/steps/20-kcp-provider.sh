# Registers the eventing API in kcp: a provider workspace that exports it and
# binds it itself. Platform admins create their Subscriptions in there.

log "Registering eventing.platform-mesh.io in kcp"
kcp root:providers apply -f - >/dev/null <<EOF
apiVersion: tenancy.kcp.io/v1alpha1
kind: Workspace
metadata:
  name: eventing
spec:
  type:
    name: provider
    path: root
EOF
kcp root:providers wait --for=jsonpath='{.status.phase}'=Ready workspace/eventing --timeout=120s >/dev/null

kcp root:providers:eventing apply -f "$ROOT/config/kcp/provider" >/dev/null
# kcp creates an endpoint slice for the export; the controller watches through it.
wait_for "APIExportEndpointSlice" 60 \
  kcp root:providers:eventing get apiexportendpointslice eventing.platform-mesh.io

kcp root:providers:eventing apply -f - >/dev/null <<EOF
apiVersion: apis.kcp.io/v1alpha2
kind: APIBinding
metadata:
  name: eventing.platform-mesh.io
spec:
  reference:
    export:
      name: eventing.platform-mesh.io
      path: root:providers:eventing
EOF
kcp root:providers:eventing wait --for=condition=Ready apibinding/eventing.platform-mesh.io --timeout=60s >/dev/null
