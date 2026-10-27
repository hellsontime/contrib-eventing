# Subscribes the event-display sink to membership events of all orgs, as a
# platform admin would: with a Subscription in the eventing workspace.

log "Subscribing event-display to membership events"
kind_kubectl apply -f "$ROOT/config/samples/event-display.yaml" >/dev/null
kind_kubectl -n eventing-demo-sink rollout status deploy/event-display --timeout=120s >/dev/null

kcp root:providers:eventing apply -f "$ROOT/config/samples/subscription.yaml" >/dev/null
kcp root:providers:eventing wait --for=condition=Ready subscription/memberships-to-event-display --timeout=120s >/dev/null

cat <<EOF

Eventing is set up. Give a user a role and watch the sink:

  ./hack/add-member.sh
  kubectl --context $KUBE_CONTEXT -n eventing-demo-sink logs deploy/event-display

EOF
