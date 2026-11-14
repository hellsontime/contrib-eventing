# Starts a registry on the kind network for the chart. Flux in the cluster
# pulls from kind-registry:5000, we push to localhost:$REGISTRY_PORT.

if [[ -z $(docker ps -q -f "name=^$REGISTRY$") ]]; then
  log "Starting registry $REGISTRY"
  docker rm -f "$REGISTRY" >/dev/null 2>&1 || true
  docker run -d --restart=always --name "$REGISTRY" --network kind -p "127.0.0.1:$REGISTRY_PORT:5000" registry:2 >/dev/null
fi
wait_for "registry on localhost:$REGISTRY_PORT" 30 curl -sf "http://localhost:$REGISTRY_PORT/v2/"
