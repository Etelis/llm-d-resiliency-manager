# Validation

## This manager

The Go controller is tested locally with simulated engine and routing endpoints.
These tests exercise the actual HTTP adapters, shared recovery IDs, stale routing
receipts, and withholding admission until survivor inference succeeds. Unit tests
cover peer evidence, journal failures, interrupted dispatch, recovery deadlines,
membership changes, and refusing a second removal.

```sh
make test vet build
go test -v ./test/integration
```

These are CPU tests. The manager, its Kubernetes Lease/journal integration, and a
real routing acknowledgement adapter have not yet been validated together on a
GPU cluster. Container compilation in CI is not a deployment test.

## Earlier engine experiments

The phase 1 investigation on 2026-09-10 used a separate Python test driver, not
this manager: 4 NVIDIA H200 GPUs across two nodes, DP=EP=4, TP=1,
DeepSeek-V2-Lite BF16, NIXL EP, eager execution, and redundant experts with
synchronous EPLB. LWS retained `RecreateGroupOnPodRestart` throughout.

The tested source was `0570fb91c5489aa0d9b5b85e438133449268e04d`, based on
`d9105ea8001e0a6d77a96327d17515bb5791fb36`, with the applied runtime patch digest
`e34639a0d93f72d456297085e3fc71bcbf3294edfe9b8c56684b37fb435c0402`.
It incorporated FT development at `bcaa5c17ef5d994a53b96b937a8e26ce0a3bbe89`
and supervisor development at `61950f9589938ffc73e4f4eaf4181c39f66afbaf`.
Those are historical revisions, not claims about the current PR heads. The full
engine patch and driver are not packaged in this controller repository.

| Injected fault | Observed result | Consequence for the manager |
| --- | --- | --- |
| GPU worker SIGKILL | Survivors resumed; 24/24 checked outputs matched baseline; Pods remained running | Single-rank removal is a viable starting case |
| EngineCore SIGKILL | Survivors resumed; 24/24 outputs matched; an orphan GPU worker remained | Exclusion does not reclaim the failed process's resources |
| API frontend SIGKILL | Routing failed over while all EP workers remained active | Frontend loss alone must not shrink EP |
| Persistent worker SIGSTOP | Survivors executed inference, but the stalled rank kept reporting healthy; 43/125 settled gateway requests timed out | Explicit routing exclusion is required |
| Transient SIGSTOP/SIGCONT followed by retry | Both measured runs failed at a later EPLB rebalance with `NIXL_ERR_REMOTE_DISCONNECT` | Do not enable automatic retry based on initial health alone |
| Container termination or Pod deletion | LWS replaced the full group and inference resumed | Keep the existing reset fallback |

These were process fault injections, not physical network failures. They do not
validate DeepEP, CUDA graphs, rank-zero failure, repeated removals, or rejoin.

## Before enabling active recovery in a fleet

Run the manager with the actual routing adapter and patched engine. Repeat worker
and EngineCore crashes, then hold a worker stopped while its API still reports
healthy. Verify every router excludes it, check survivor outputs against a baseline,
and continue inference through scheduled EPLB rebalances.

Interrupt the manager before dispatch, during dispatch, during verification and
after routing publication. Confirm no uncertain command is replayed. Exercise
Lease loss, journal conflicts, lost routing receipts, Pod replacement and IP reuse.
Finally, verify the unchanged LWS full-group fallback.

Record the manager and engine revisions, image digests, GPU model/count, commands,
fault injection method, Pod UIDs, recovery timings, request failures and output
comparisons. Keep credentials and internal infrastructure identifiers out of public
reports. This acceptance work remains open.
