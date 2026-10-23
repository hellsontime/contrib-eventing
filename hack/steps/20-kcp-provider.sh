# Registers the eventing API in kcp: a provider workspace that exports it, and
# a policy that lets organizations bind it.

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
kcp root:orgs apply -f "$ROOT/config/kcp/orgs" >/dev/null
