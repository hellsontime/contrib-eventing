# Builds the image, loads it into the kind node and pushes it to the registry.
# The chart runs the loaded one with pullPolicy IfNotPresent, the OCM component
# pins the pushed one by digest. The tag is a hash of the Go sources: a code
# change rolls out new pods, an unchanged checkout changes nothing.

source_hash=$(cd "$ROOT" && find go.mod go.sum main.go cmd api internal config/kcp -type f -print0 | sort -z |
  xargs -0 sha256sum | sha256sum | cut -c1-12)
IMAGE_TAGGED=${IMAGE%:*}:dev-$source_hash
image_name=${IMAGE_TAGGED##*/} && image_name=${image_name%:*}

# registry_digest <repository> <tag>: the digest of the tag in the registry, if it is there.
registry_digest() {
  curl -sfI -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json' \
    "http://localhost:$REGISTRY_PORT/v2/$1/manifests/$2" | tr -d '\r' | awk 'tolower($1) == "docker-content-digest:" {print $2}'
}

build() {
  docker image inspect "$IMAGE_TAGGED" >/dev/null 2>&1 && return
  log "Building the image"
  docker build -q -t "$IMAGE_TAGGED" "$ROOT" >/dev/null
}

if docker exec "$KIND_NODE" ctr --namespace=k8s.io images ls -q | grep -qx "docker.io/${IMAGE_TAGGED}\|${IMAGE_TAGGED}"; then
  log "Image $IMAGE_TAGGED is already loaded"
else
  build
  if command -v kind >/dev/null; then
    kind load docker-image --name "${KIND_NODE%-control-plane}" "$IMAGE_TAGGED" >/dev/null
  else
    # This is what kind load does under the hood.
    docker save "$IMAGE_TAGGED" | docker exec -i "$KIND_NODE" ctr --namespace=k8s.io images import - >/dev/null
  fi
fi

IMAGE_DIGEST=$(registry_digest "$image_name" "${IMAGE_TAGGED##*:}")
if [[ -z $IMAGE_DIGEST ]]; then
  build
  log "Pushing the image to $REGISTRY"
  docker tag "$IMAGE_TAGGED" "localhost:$REGISTRY_PORT/$image_name:${IMAGE_TAGGED##*:}"
  docker push -q "localhost:$REGISTRY_PORT/$image_name:${IMAGE_TAGGED##*:}" >/dev/null
  IMAGE_DIGEST=$(registry_digest "$image_name" "${IMAGE_TAGGED##*:}")
  [[ -n $IMAGE_DIGEST ]] || die "image $image_name:${IMAGE_TAGGED##*:} is not in $REGISTRY after the push"
fi
