# Receiving events

This is for platform admins who want the membership events of the platform
delivered to their own HTTP endpoint.

## 1. Provide an endpoint

Your endpoint receives one CloudEvent per `POST`, in CloudEvents HTTP binary
mode: the attributes come as `ce-*` headers (`ce-id`, `ce-type`, `ce-source`,
`ce-subject`, `ce-time`, `ce-pmorg`), the data as the JSON body.
[api/openapi/subscriber.yaml](../api/openapi/subscriber.yaml) describes the
requests and the answers your endpoint must give; [contract.md](contract.md)
explains the fields.

The rules that matter:

- Answer with a 2xx and an **empty body**, quickly. A body that is not a
  CloudEvent counts as a failed delivery. A body that is a CloudEvent is taken
  as a reply; replies are blocked and dropped, they never reach anyone.
- 404, 408, 409, 429 and 5xx are retried with backoff; any other status is not.
- The same event can arrive more than once. Drop events whose `ce-id` you have
  already seen. There is no order between events.
- The endpoint must be reachable from inside the cluster, for example as a
  Kubernetes Service. Requests come without credentials.
- Its host must be on the platform operator's allowlist (the controller's
  `--allowed-sink-hosts`: host names, or `*.suffix` for everything below a
  domain). Events carry user ids, so the operator decides where they may go.
  Otherwise the Subscription stays `Ready=False` with reason `SinkNotAllowed`.
  The same goes for the dead letter sink. The local install allows
  `*.eventing-demo-sink.svc.cluster.local`.

## 2. Create a Subscription

Subscriptions live in the workspace `root:providers:eventing`, which only
platform admins can reach. With the repo's scripts on a local setup:

```sh
source hack/lib.sh
kcp root:providers:eventing apply -f - <<EOF
apiVersion: eventing.platform-mesh.io/v1alpha1
kind: Subscription
metadata:
  name: my-consumer
spec:
  filter:
    types:
      - io.platform-mesh.iam.membership.granted.v1alpha1
      - io.platform-mesh.iam.membership.revoked.v1alpha1
      - io.platform-mesh.iam.account.members.v1alpha1
    orgs: [acme]   # optional; without it you get all orgs
  sink:
    uri: http://my-consumer.my-namespace.svc.cluster.local
  delivery:
    retry: 3
    timeout: PT10S   # optional, per request
    deadLetterSink:  # optional
      uri: http://my-dead-letters.my-namespace.svc.cluster.local
EOF
kcp root:providers:eventing get subscriptions
```

`delivery` is optional. `retry` is the number of retries after the first
failed attempt; `backoffPolicy` (`linear` or `exponential`) and `backoffDelay`
(ISO 8601, e.g. `PT0.5S`) shape the wait in between. `timeout` limits a single
request. An event that still failed after the last retry goes to
`deadLetterSink`, with Knative's `knativeerrordest`, `knativeerrorcode` and
`knativeerrordata` attributes added. Without a dead letter sink it is dropped.

The Subscription is ready when `READY` is `True`; otherwise `REASON` says what
is missing. Events start arriving with the next membership change. Deleting the
Subscription stops delivery.

## Limits in milestone 1

- Events not yet delivered are lost when the in-memory channel restarts.
