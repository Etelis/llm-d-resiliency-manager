# Recovery coordination

The manager connects three responsibilities that currently live apart:
engine recovery, routing admission, and Kubernetes workload lifecycle. It runs
outside the inference Pods so a failed worker does not take the coordinator down.

## Phase 1

One manager configuration describes one EP group. Discovery maps each Pod's LWS
worker index to a fixed number of ranks, with one HTTP frontend per rank on
consecutive ports. Ranks must be dense from zero. This first adapter targets the
DP=EP, TP=1 layout used in our experiments.

The controller waits for a complete, healthy baseline. It then requires repeated
observations in which at least two peers mask the same non-master rank and all
survivors report unhealthy at the FT barrier. A frontend disappearing by itself
does not establish that its EP worker has failed.

An operation proceeds as follows:

1. Persist a shared operation ID and the original process membership.
2. Publish an empty routing allowlist and wait for acknowledgement.
3. Recheck peer evidence and Pod membership, then journal the dispatch intent.
4. Send `scale_down` concurrently to every survivor with the same request ID.
5. Require consecutive healthy status and successful completion checks on every
   survivor. An HTTP 202 from recovery is only acceptance.
6. Publish the survivor allowlist and wait for routing acknowledgement.

The group then remains degraded. A subsequent survivor failure causes quarantine;
there is no second removal, automatic `retry`, master relocation, or rejoin.
Recovery pauses group admission, so this is not a zero-downtime guarantee.
It neither reconstructs lost KV state nor guarantees completion of in-flight requests.

## Ownership and restart behavior

Active mode uses a Kubernetes Lease and a ConfigMap journal named
`resiliency-<group>`. Journal updates use Kubernetes resource versions. The manager
never changes LWS ownership or restart policy. LWS continues to replace the group
when its normal restart conditions occur; entering a blocked state does not itself
trigger a reset. A contained failure may therefore need operator intervention.

Membership includes Pod UID, container ID and endpoint, hashed into an incarnation.
It does not identify a child process relaunched inside the same container; that
must be disabled for this adapter until engine generations are available.
A changed or incomplete membership during an operation quarantines the old cohort.
The controller only starts over once every original Pod UID has been replaced.
It does not interpret a partial Pod replacement as a successful rejoin.

The vLLM protocol has no durable receipt for a completed operation. If the manager
restarts after recording dispatch intent but before recording its outcome, it
quarantines and stops automatic recovery. It does not resend the command. A saved
`recovering` state can continue verification without redispatch. A saved routing
publication can be retried because the routing contract requires idempotency.

Lease election prevents ordinary concurrent leaders; it cannot fence a paused
former leader inside vLLM. Checking Pod UIDs also cannot eliminate the race between
discovery and an HTTP request to a reused address. Engine-side epoch validation is
a phase 2 requirement. The initial controller is not partition-safe HA recovery.

## Engine compatibility

This adapter targets the experimental API exercised in our phase 1 vLLM work:

- `GET /v1/fault_tolerance/status`: schema version 1, one engine per frontend,
  with the original global rank in `id` and a peer mask when unhealthy.
- `POST /v1/fault_tolerance/apply`: `scale_down`, `removed_dp_ranks`, and a shared,
  nonempty `request_id`. HTTP 202 must echo that request ID.
- `POST /v1/completions`: a short, deterministic completion used to check that
  every survivor can actually execute inference.

It is not an adapter for an arbitrary stock vLLM release. Relevant development is
in [vLLM #46370](https://github.com/vllm-project/vllm/pull/46370) and
[vLLM #54963](https://github.com/vllm-project/vllm/pull/54963); compatibility is with
the tested API described here, not every revision of those PRs. The
[validation record](validation.md) identifies the historical tested source.

The engine must have sufficient redundant experts and a compatible EP recovery
backend. Neither capability is inferred from a successful status request. The
completion check establishes basic progress, not numerical correctness or durable
recovery across later EPLB rebalances.

## Enabling recovery

First provide a [routing adapter](routing.md) and validate its acknowledgement
semantics with every router serving the group. Stock health filtering is insufficient.
Use one manager configuration and one lifecycle owner for a group; overlapping
selectors or different group names pointing at the same ranks are unsupported.

Set `enableRecovery: true`, `model` to the served model name, and `routingURL` to
the adapter's publication endpoint. Adjust the resource names in
`deploy/active-rbac.yaml` to `resiliency-<group>` and apply it in the manager's
namespace. It grants creation and named access to the operation ConfigMap and
Lease, with no Pod or LWS writes. Kubernetes RBAC cannot restrict `create` by
resource name, so creation is namespace-scoped.

Optional `engineTokenFile` and `routingTokenFile` settings read bearer tokens from
mounted files at startup. Engine discovery currently uses Pod HTTP endpoints;
keep these administrative endpoints on a trusted network. Routing supports HTTPS
with system trust roots. Requests have bounded timeouts, reject redirects, and do
not log response bodies or tokens.

Do not delete the journal to retry an uncertain operation on the same processes.
Keep the group quarantined and use an operator-controlled full replacement.
Switching back to observation does not clear existing routing exclusions.

## Package boundaries

`pkg/recovery` depends on four small interfaces: `Engine`, `Routing`, `Workload`
and `Journal`. Kubernetes discovery does not decide recovery policy; the engine
adapter does not control routing. Phase 2 can extend these boundaries once its
lifecycle contracts are agreed, without adding another orchestration service.
The interfaces and wire contract are alpha and may change during that work.
