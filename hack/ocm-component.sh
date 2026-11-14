#!/usr/bin/env bash
# Builds the OCM component of a release into a CTF archive:
#   hack/ocm-component.sh <version> <image reference> <ctf path>
# Then: ocm transfer ctf --overwrite <ctf path> <registry>
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
(($# == 3)) || { echo "usage: $0 <version> <image reference> <ctf path>" >&2; exit 1; }
version=$1 image=$2 ctf=$3
command -v ocm >/dev/null || { echo "error: ocm is not installed" >&2; exit 1; }
[[ $image != *@* && ${image##*/} == *:* ]] || { echo "error: $image needs a tag and no digest" >&2; exit 1; }

# Flux needs the chart's version to be its OCI tag. The chart's image is the
# component's, so a fork or a local registry pulls what it built. Stamped in a
# copy, the checkout stays as it is.
tmp=$(mktemp -d)
trap 'rm -r "$tmp"' EXIT
mkdir -p "$tmp/charts"
cp -R "$root/ocm" "$tmp/ocm"
cp -R "$root/charts/eventing" "$tmp/charts/eventing"
sed -e "s/^version: .*/version: $version/" -e "s/^appVersion: .*/appVersion: \"$version\"/" \
  "$root/charts/eventing/Chart.yaml" >"$tmp/charts/eventing/Chart.yaml"
# A plain-http registry is given as http://, which pods don't take.
repository=${image%:*} && repository=${repository#http://}
sed -e "s|^  repository: .*|  repository: $repository|" -e "s|^  tag: .*|  tag: \"${image##*:}\"|" \
  "$root/charts/eventing/values.yaml" >"$tmp/charts/eventing/values.yaml"
grep -qx "  repository: $repository" "$tmp/charts/eventing/values.yaml" ||
  { echo "error: could not set image.repository in values.yaml" >&2; exit 1; }

ocm add components --create --templater=go --file "$ctf" "$tmp/ocm/component-constructor.yaml" -- \
  VERSION="$version" IMAGE="$image"
