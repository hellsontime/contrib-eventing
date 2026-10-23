# Builds the image and loads it into the kind node, so no registry is needed.
# The tag is a hash of the Go sources: a code change rolls out new pods, an
# unchanged checkout changes nothing.

source_hash=$(cd "$ROOT" && find go.mod go.sum cmd api internal -type f -print0 | sort -z |
  xargs -0 sha256sum | sha256sum | cut -c1-12)
IMAGE_TAGGED=${IMAGE%:*}:dev-$source_hash

if docker exec "$KIND_NODE" ctr --namespace=k8s.io images ls -q | grep -qx "docker.io/${IMAGE_TAGGED}\|${IMAGE_TAGGED}"; then
  log "Image $IMAGE_TAGGED is already loaded"
else
  log "Building the image"
  docker build -q -t "$IMAGE_TAGGED" "$ROOT" >/dev/null
  if command -v kind >/dev/null; then
    kind load docker-image --name "${KIND_NODE%-control-plane}" "$IMAGE_TAGGED" >/dev/null
  else
    # This is what kind load does under the hood.
    docker save "$IMAGE_TAGGED" | docker exec -i "$KIND_NODE" ctr --namespace=k8s.io images import - >/dev/null
  fi
fi
