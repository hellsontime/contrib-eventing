# Event contract

This is what subscribers can rely on. It is a draft (`v1alpha1`) and may still change.

Every event is a [CloudEvent 1.0](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md) delivered over HTTP. The contract does not depend on which system produced the change: today membership events come from OpenFGA, but another adapter could produce the same events from a different authorization engine.

## Membership events

Sent when a user gets or loses a role on an account.

| Type | Meaning |
| --- | --- |
| `io.platform-mesh.iam.membership.granted.v1alpha1` | The user now has the role |
| `io.platform-mesh.iam.membership.revoked.v1alpha1` | The user no longer has the role |

### Example

```json
{
  "specversion": "1.0",
  "id": "25fdb262e1c7303601770040f01083d9",
  "source": "platform-mesh.io/orgs/acme/iam",
  "type": "io.platform-mesh.iam.membership.granted.v1alpha1",
  "subject": "accounts/team-a",
  "time": "2026-10-01T09:12:44Z",
  "pmorg": "acme",
  "datacontenttype": "application/json",
  "data": {
    "org": "acme",
    "account": "team-a",
    "role": "member",
    "user": { "id": "alice@example.com" }
  }
}
```

### Attributes

| Attribute | Value |
| --- | --- |
| `id` | Unique per change and the same on every redelivery. Use it to drop duplicates. Do not parse it. |
| `source` | `platform-mesh.io/orgs/<org>/iam` |
| `type` | one of the types above |
| `subject` | `accounts/<account>` |
| `time` | when the change happened |
| `pmorg` | the org name, so subscribers can filter without reading `data` |

### Data

| Field | Required | Meaning |
| --- | --- | --- |
| `org` | yes | org name |
| `account` | yes | account name |
| `role` | yes | Platform Mesh role, for example `owner` or `member` |
| `user.id` | yes | the user as Platform Mesh identifies them: the value of the user claim the platform admin configured, `email` by default. Invited users who have not logged in yet are identified the same way. |

## What subscribers should expect

- **Duplicates.** Delivery is at least once. Drop events whose `id` you have already seen.
- **No ordering.** A role change arrives as a revoke of the old role and a grant of the new one, in either order.
- **Account deletion** shows up as one revoke per member. There is no separate event for it yet.
- **Org deletion** produces no membership events.
- **Account creation** shows up as an owner grant for the creator.

## Open questions

- Is a stream of changes enough, or do subscribers also need a full snapshot of an account's members to resync?
- Is the role list fixed, or can adopters define their own roles?
- Accounts can be nested, so the account name alone is not unique within an org. `subject` will carry a stable account id; its format is still open.

## Versioning

Proposed rule: the version is part of the type. Adding an optional field keeps the version. Removing or changing the meaning of a field creates a new type version, and both versions are sent during a transition.
