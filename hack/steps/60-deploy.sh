# Deploys the subscription controller, the OpenFGA adapter and the ingress.

log "Deploying $IMAGE_TAGGED"
TRAEFIK_IP=$(exposure traefikClusterIP)
# Users are identified the same way security-operator identifies them.
USER_CLAIM=$(kind_kubectl -n platform-mesh-system get deploy security-operator -o json |
  jq -r '[.spec.template.spec.containers[0].args[] | select(startswith("--user-claim=")) | ltrimstr("--user-claim=")][0] // "email"')
sed -e "s|\${IMAGE}|$IMAGE_TAGGED|g" -e "s|\${BASE_DOMAIN}|$BASE_DOMAIN|g" -e "s|\${TRAEFIK_IP}|$TRAEFIK_IP|g" \
  -e "s|\${USER_CLAIM}|$USER_CLAIM|g" "$ROOT/config/deploy/eventing.yaml" | kind_kubectl apply -f - >/dev/null
kind_kubectl -n "$NS" rollout status deploy/subscription-controller deploy/openfga-adapter deploy/ingress --timeout=120s >/dev/null

# The broker channel delivers in the background (see internal/delivery/knative).
# Knative takes that setting from the Broker only when it creates the channel,
# and the dispatcher reads it only when it first sees the channel, so channels
# from older installs are switched here.
channel=default-kne-trigger
if kind_kubectl -n "$NS" get inmemorychannel "$channel" >/dev/null 2>&1 &&
  [[ $(kind_kubectl -n "$NS" get inmemorychannel "$channel" -o jsonpath='{.metadata.annotations.messaging\.knative\.dev/async-handler}') != true ]]; then
  log "Switching the broker channel to background delivery"
  kind_kubectl -n "$NS" annotate inmemorychannel "$channel" messaging.knative.dev/async-handler=true --overwrite >/dev/null
  kind_kubectl -n knative-eventing rollout restart deploy/imc-dispatcher >/dev/null
  kind_kubectl -n knative-eventing rollout status deploy/imc-dispatcher --timeout=120s >/dev/null
fi
