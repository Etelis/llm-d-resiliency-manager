# Contributing

Open an issue before changing recovery policy or an adapter contract. Check existing
issues and pull requests for overlap. Keep changes focused and explain the failure
they address.

Use Go 1.26.8 or newer and run `make fmt test vet build`. Extend nearby tests for
distinct failure behavior; avoid tests that only repeat implementation details.
GPU results should name the source revision, hardware, commands and observed
outcome. Distinguish simulated faults from real failures and local tests from
cluster validation.

Pull requests should link the issue, briefly describe the problem and solution,
and finish with a Tests section. Comments should explain only non-obvious behavior
or constraints.

Contributions are licensed under Apache 2.0. Sign off commits with `git commit -s`
to certify the [Developer Certificate of Origin](https://developercertificate.org/).
Do not add someone else's sign-off. Review ownership is listed in [OWNERS](OWNERS).

This repository is being prepared for discussion under the
[llm-d project process](https://github.com/llm-d/llm-d/blob/main/PROJECT.md).
An incubator transfer, additional maintainers and graduation criteria need to be
agreed with that community; the repository name does not imply acceptance.
