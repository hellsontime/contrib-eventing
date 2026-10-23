#!/usr/bin/env bash
# Regenerates deepcopy code, the CRD and the kcp APIResourceSchema from api/.
# Tool versions match the Platform Mesh monorepo (tools/Taskfile.yaml).
set -euo pipefail
cd "$(dirname "$0")/.."

CONTROLLER_GEN="go run sigs.k8s.io/controller-tools/cmd/controller-gen@v0.21.0"
APIGEN="go run github.com/kcp-dev/sdk/cmd/apigen@v0.33.1"

$CONTROLLER_GEN object:headerFile=hack/boilerplate.go.txt paths=./api/...
$CONTROLLER_GEN crd paths=./api/... output:crd:dir=config/crd

# apigen turns the CRD into an APIResourceSchema, the form kcp exports APIs in.
# Schemas are immutable, so apigen puts the date and commit into the name. The
# APIExport has to point at that name; we keep our hand-written export and
# only update the reference.
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
$APIGEN --input-dir config/crd --output-dir "$tmp"
schema=config/kcp/provider/apiresourceschema-subscriptions.yaml
mv "$tmp"/apiresourceschema-subscriptions.eventing.platform-mesh.io.yaml "$schema"
name=$(sed -n 's/^  name: //p' "$schema" | head -1)
sed -i "s/^\(      schema: \).*/\1$name/" config/kcp/provider/apiexport.yaml
