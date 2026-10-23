#!/usr/bin/env bash
# Adds eventing to a local Platform Mesh. Safe to run again.
# Settings can be overridden with environment variables, see hack/lib.sh.
source "$(dirname "$0")/lib.sh"

for step in "$ROOT"/hack/steps/*.sh; do
  source "$step"
done
