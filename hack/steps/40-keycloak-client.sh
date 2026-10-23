# Creates the Keycloak client the OpenFGA adapter uses to turn emails into
# user ids: a service account in the master realm with the admin role, the
# same way security-operator and iam-service get theirs. Reading users is all
# the adapter needs; narrowing the role is open work.

log "Creating the Keycloak client for the OpenFGA adapter"
client_id=contrib-eventing
uuid=$(keycloak GET "/admin/realms/master/clients?clientId=$client_id" | jq -r '.[0].id // empty')
if [[ -z $uuid ]]; then
  keycloak POST /admin/realms/master/clients -d "$(jq -nc --arg id "$client_id" '{
    clientId: $id, publicClient: false, serviceAccountsEnabled: true,
    standardFlowEnabled: false, directAccessGrantsEnabled: false
  }')" >/dev/null
  uuid=$(keycloak GET "/admin/realms/master/clients?clientId=$client_id" | jq -r '.[0].id')
fi

sa_user=$(keycloak GET "/admin/realms/master/clients/$uuid/service-account-user" | jq -r .id)
admin_role=$(keycloak GET /admin/realms/master/roles/admin)
keycloak POST "/admin/realms/master/users/$sa_user/role-mappings/realm" -d "[$admin_role]" >/dev/null

secret=$(keycloak GET "/admin/realms/master/clients/$uuid/client-secret" | jq -r .value)
kind_kubectl -n "$NS" create secret generic contrib-eventing-keycloak \
  --from-literal=client-secret="$secret" --dry-run=client -o yaml | kind_kubectl apply -f - >/dev/null
