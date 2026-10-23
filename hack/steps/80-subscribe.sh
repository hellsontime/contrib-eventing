# Binds the eventing API in the demo organization and subscribes the
# event-display sink to membership events.

log "Subscribing event-display to membership events of $ORG"
kcp "root:orgs:$ORG" apply -f - >/dev/null <<EOF
apiVersion: apis.kcp.io/v1alpha2
kind: APIBinding
metadata:
  name: eventing.platform-mesh.io
spec:
  reference:
    export:
      name: eventing.platform-mesh.io
      path: root:providers:eventing
  permissionClaims:
    - group: core.kcp.io
      resource: logicalclusters
      verbs: ["get", "list", "watch"]
      selector:
        matchAll: true
      state: Accepted
EOF
kcp "root:orgs:$ORG" wait --for=condition=Ready apibinding/eventing.platform-mesh.io --timeout=60s >/dev/null

kind_kubectl apply -f "$ROOT/config/samples/event-display.yaml" >/dev/null
kind_kubectl -n eventing-demo-sink rollout status deploy/event-display --timeout=120s >/dev/null

kcp "root:orgs:$ORG" apply -f "$ROOT/config/samples/subscription.yaml" >/dev/null
kcp "root:orgs:$ORG" wait --for=condition=Ready subscription/memberships-to-event-display --timeout=120s >/dev/null

cat <<EOF

Eventing is set up. Give a user a role and watch the sink:

  ./hack/add-member.sh
  kubectl --context $KUBE_CONTEXT -n eventing-demo-sink logs deploy/event-display

EOF
