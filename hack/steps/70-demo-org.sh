# Creates a demo organization, so there are memberships to watch.
#
# This follows installation/organization-onboarding.md in helm-charts, with one
# shortcut: instead of logging in as the new user, the kcp admin creates the
# Account while impersonating them. The Account webhook then records the user
# as creator, and Platform Mesh invites them into the organization's realm.

log "Creating demo organization $ORG for $DEMO_USER"
kcp root:orgs apply --as "$DEMO_USER" -f - >/dev/null <<EOF
apiVersion: core.platform-mesh.io/v1alpha1
kind: Account
metadata:
  name: $ORG
spec:
  type: org
  displayName: Eventing demo
EOF
kcp root:orgs wait --for=condition=Ready "account/$ORG" --timeout=300s >/dev/null
