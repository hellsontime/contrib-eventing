# Checks that Platform Mesh is up and stores the credentials the other steps need.

for tool in kubectl docker curl jq base64; do
  command -v "$tool" >/dev/null || die "$tool is not installed"
done

kind_kubectl -n platform-mesh-system wait --for=condition=Ready platformmesh/platform-mesh --timeout=10s >/dev/null ||
  die "PlatformMesh is not Ready in context $KUBE_CONTEXT"

mkdir -p "$SECRET_DIR"
kind_kubectl -n platform-mesh-system get secret kubeconfig-kcp-admin -o jsonpath='{.data.kubeconfig}' |
  base64 -d >"$SECRET_DIR/kcp-admin.kubeconfig"
kind_kubectl -n platform-mesh-system get secret domain-certificate-ca -o jsonpath='{.data.tls\.crt}' |
  base64 -d >"$SECRET_DIR/domain-ca.crt"

kcp root get workspace orgs >/dev/null ||
  die "cannot reach kcp at https://$INGRESS_ADDRESS:$PORT as $KCP_HOST"
log "Platform Mesh at $BASE_DOMAIN:$PORT is ready"
