# Roadmap

Each milestone ends with something that runs on a local kind setup.

1. **Membership events.** The OpenFGA adapter publishes `membership.granted`
   and `membership.revoked` for every org that subscribed ([contract](contract.md)).
   Orgs declare Subscriptions in kcp, Knative delivers to their HTTP endpoints,
   and an ingress authenticates every sender before an event reaches a broker.
   Providers get a way to publish their own events through that ingress. One
   script adds all of this to a local Platform Mesh.
2. **Ready for a first consumer.** Subscription targets limited by an
   allowlist the platform operator controls, a durable broker class instead of
   the in-memory channel, dead letter sinks, and the contract checked with the
   first consumer (snapshots, stable account ids).
3. **Keycloak events.** Login, user created and user deleted, from a Keycloak
   adapter. A prototype for login exists.
4. **Installable module.** Published images and chart, install through
   `ManagedProvider`, automatic binding for new orgs, more than one shard,
   installations other than kind.
5. **Later.** Account-level and installation-wide subscriptions, kcp resource
   events.

Delivery stays HTTP to the subscriber in all of these. Bridges to GCP Pub/Sub,
Kafka and the like run on the subscriber side.
