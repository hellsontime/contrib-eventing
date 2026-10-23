# Creates the demo organization.
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

# Membership events carry the user's Keycloak id once the user exists in the
# org's realm, so wait for the invite to create them.
demo_user_id() {
  keycloak GET "/admin/realms/$ORG/users?exact=true&email=$DEMO_USER" | jq -er '.[0].id'
}
wait_for "user $DEMO_USER in realm $ORG" 180 demo_user_id
