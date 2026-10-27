# Deploys the subscription controller, the OpenFGA adapter and the ingress.

log "Deploying $IMAGE_TAGGED"
TRAEFIK_IP=$(exposure traefikClusterIP)
# Users are identified the same way security-operator identifies them.
USER_CLAIM=$(kind_kubectl -n platform-mesh-system get deploy security-operator -o json |
  jq -r '[.spec.template.spec.containers[0].args[] | select(startswith("--user-claim=")) | ltrimstr("--user-claim=")][0] // "email"')
sed -e "s|\${IMAGE}|$IMAGE_TAGGED|g" -e "s|\${BASE_DOMAIN}|$BASE_DOMAIN|g" -e "s|\${TRAEFIK_IP}|$TRAEFIK_IP|g" \
  -e "s|\${USER_CLAIM}|$USER_CLAIM|g" "$ROOT/config/deploy/eventing.yaml" | kind_kubectl apply -f - >/dev/null
kind_kubectl -n "$NS" rollout status deploy/subscription-controller deploy/openfga-adapter deploy/ingress --timeout=120s >/dev/null
