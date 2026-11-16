# Checks that Platform Mesh is up and stores the credentials the other steps need.

for tool in kubectl helm docker curl jq base64; do
  command -v "$tool" >/dev/null || die "$tool is not installed"
done

kind_kubectl -n platform-mesh-system wait --for=condition=Ready platformmesh/platform-mesh --timeout=10s >/dev/null ||
  die "PlatformMesh is not Ready in context $KUBE_CONTEXT"

# The platform-mesh-operator installs ManagedProviders with Flux and the
# ocm-controller.
for crd in ocirepositories.source.toolkit.fluxcd.io helmreleases.helm.toolkit.fluxcd.io managedproviders.providers.platform-mesh.io \
  components.delivery.ocm.software resources.delivery.ocm.software; do
  kind_kubectl get crd "$crd" >/dev/null 2>&1 || die "CRD $crd is missing, Platform Mesh is installed without it"
done

mkdir -p "$SECRET_DIR"
kind_kubectl -n platform-mesh-system get secret kubeconfig-kcp-admin -o jsonpath='{.data.kubeconfig}' |
  base64 -d >"$SECRET_DIR/kcp-admin.kubeconfig"
kind_kubectl -n platform-mesh-system get secret domain-certificate-ca -o jsonpath='{.data.tls\.crt}' |
  base64 -d >"$SECRET_DIR/domain-ca.crt"

kcp root get workspace orgs >/dev/null ||
  die "cannot reach kcp at https://$INGRESS_ADDRESS:$PORT as $KCP_HOST"
log "Platform Mesh at $BASE_DOMAIN:$PORT is ready"
