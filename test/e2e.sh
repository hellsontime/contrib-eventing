#!/usr/bin/env bash
# End-to-end check against an installation made by hack/install.sh. Leaves
# the installation as it found it.
source "$(dirname "$0")/../hack/lib.sh"
source "$ROOT/hack/steps/00-preflight.sh" >/dev/null

failed=0
pass() { printf '  ok    %s\n' "$*"; }
fail() { printf '  FAIL  %s\n' "$*"; failed=1; }

sub=memberships-to-event-display
sink_logs() { kind_kubectl -n eventing-demo-sink logs deploy/event-display; }
count() { sink_logs | grep -c "type: io.platform-mesh.iam.membership.$1" || true; }
more_than() { (($(count "$1") > $2)); }
# The last event of a type, as event-display prints it.
last_event() { sink_logs | grep -A22 "type: io.platform-mesh.iam.membership.$1" | tail -23; }
add_member() { "$ROOT/hack/add-member.sh" "$@" >/dev/null; }

log "Subscription is ready"
org=$(kcp "root:orgs:$ORG" get subscription $sub -o jsonpath='{.status.org}')
ready=$(kcp "root:orgs:$ORG" get subscription $sub -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')
[[ $org == "$ORG" && $ready == True ]] && pass "Ready, status.org=$org" || fail "org=$org ready=$ready"

log "A new member is announced with their Keycloak id"
user_id=$(keycloak GET "/admin/realms/$ORG/users?exact=true&email=$DEMO_USER" | jq -r '.[0].id')
# Start from a known state: the demo user is not a member yet.
add_member --remove "$DEMO_USER" member
sleep 10
before=$(count granted)
add_member "$DEMO_USER" member
wait_for "membership.granted at the sink" 30 more_than granted "$before"
event=$(last_event granted)
grep -q "source: platform-mesh.io/orgs/$ORG/iam" <<<"$event" && pass "source" || fail "source missing"
grep -q "subject: accounts/$ORG" <<<"$event" && pass "subject" || fail "subject missing"
grep -q "pmorg: $ORG" <<<"$event" && pass "pmorg" || fail "pmorg missing"
grep -q '"role": "member"' <<<"$event" && pass "role" || fail "role missing"
grep -q "\"id\": \"$user_id\"" <<<"$event" && pass "user.id is the Keycloak id $user_id" || fail "user id missing"
grep -q '@' <<<"$(sed -n '/^Data,/,$p' <<<"$event")" && fail "email in event" || pass "no email in the event"

log "An invited user without Keycloak account is announced by email"
invited=invited-$RANDOM@example.com
before=$(count granted)
add_member "$invited" member
wait_for "membership.granted for the invited user" 30 more_than granted "$before"
grep -q "\"email\": \"$invited\"" <<<"$(last_event granted)" && pass "user.email=$invited" || fail "email missing"
before=$(count revoked)
add_member --remove "$invited" member
wait_for "membership.revoked for the invited user" 30 more_than revoked "$before"

log "Removing the role is announced"
before=$(count revoked)
add_member --remove "$DEMO_USER" member
wait_for "membership.revoked at the sink" 30 more_than revoked "$before"
grep -q "\"id\": \"$user_id\"" <<<"$(last_event revoked)" && pass "membership.revoked" || fail "revoked event wrong"

log "Restarting the adapter does not publish again"
total=$(($(count granted) + $(count revoked)))
kind_kubectl -n "$NS" rollout restart deploy/openfga-adapter >/dev/null
kind_kubectl -n "$NS" rollout status deploy/openfga-adapter --timeout=120s >/dev/null
sleep 15
[[ $(($(count granted) + $(count revoked))) == "$total" ]] && pass "no duplicates" || fail "events published again"

log "Only known publishers get events into a broker"
probe() {
  kind_kubectl -n default run "e2e-probe-$RANDOM" --rm -i --restart=Never --quiet --image=curlimages/curl:8.5.0 --command -- \
    curl -s -m 5 -o /dev/null -w '%{http_code}' -X POST "$1" -H "Ce-Id: forged" -H "Ce-Specversion: 1.0" \
    -H "Ce-Type: io.platform-mesh.iam.membership.granted.v1alpha1" -H "Ce-Source: forged" -H "Ce-Pmorg: $ORG" \
    -H "Content-Type: application/json" ${2:+-H "Authorization: Bearer $2"} -d '{}' 2>/dev/null || true
}
code=$(probe "http://broker-ingress.knative-eventing.svc.cluster.local/pm-events-$ORG/default")
[[ $code != 202 ]] && pass "direct post to the broker is blocked" || fail "broker accepted a forged event"
code=$(probe http://ingress.contrib-eventing.svc.cluster.local)
[[ $code == 401 ]] && pass "ingress without token: 401" || fail "ingress without token: $code"
token=$(kind_kubectl -n default create token default --audience contrib-eventing)
code=$(probe http://ingress.contrib-eventing.svc.cluster.local "$token")
[[ $code == 403 ]] && pass "ingress with another workload's token: 403" || fail "ingress with foreign token: $code"

log "Subscriptions outside organization workspaces are refused"
kcp "root:orgs:$ORG" apply -f - >/dev/null <<EOF
apiVersion: core.platform-mesh.io/v1alpha1
kind: Account
metadata:
  name: e2e-account
spec:
  type: account
EOF
kcp "root:orgs:$ORG" wait --for=condition=Ready account/e2e-account --timeout=180s >/dev/null
kcp "root:orgs:$ORG" get apibinding eventing.platform-mesh.io -o json |
  jq 'del(.metadata.resourceVersion, .metadata.uid, .metadata.creationTimestamp, .metadata.annotations, .status)' |
  kcp "root:orgs:$ORG:e2e-account" apply -f - >/dev/null
kcp "root:orgs:$ORG:e2e-account" wait --for=condition=Ready apibinding/eventing.platform-mesh.io --timeout=60s >/dev/null
kcp "root:orgs:$ORG:e2e-account" apply -f "$ROOT/config/samples/subscription.yaml" >/dev/null
reason_is() { [[ $(kcp "root:orgs:$ORG:e2e-account" get subscription $sub -o jsonpath='{.status.conditions[0].reason}') == "$1" ]]; }
wait_for "NotAnOrgWorkspace" 30 reason_is NotAnOrgWorkspace && pass "reason NotAnOrgWorkspace"
kcp "root:orgs:$ORG" delete account e2e-account --wait=false >/dev/null

log "No events after the Subscription is deleted"
kcp "root:orgs:$ORG" delete subscription $sub --timeout=60s >/dev/null
before=$(count granted)
add_member "$DEMO_USER" member
sleep 15
[[ $(count granted) == "$before" ]] && pass "nothing delivered" || fail "event delivered without Subscription"
add_member --remove "$DEMO_USER" member
kcp "root:orgs:$ORG" apply -f "$ROOT/config/samples/subscription.yaml" >/dev/null
kcp "root:orgs:$ORG" wait --for=condition=Ready subscription/$sub --timeout=120s >/dev/null

if ((failed)); then
  echo "e2e failed"
  exit 1
fi
echo "e2e passed"
