# Contributing

Follow the [llm-d contribution guidelines](https://github.com/llm-d/llm-d/blob/main/CONTRIBUTING.md).
Discuss policy or API changes in an issue first. Keep patches focused and extend
nearby tests for distinct failure behavior.

Run `make fmt test vet build` and sign off commits with `git commit -s`
([DCO](https://developercertificate.org/)). Pull requests should describe the
problem and change, then finish with a Tests section. GPU results must include
source revisions, hardware, commands and outcomes; label simulated failures.

[OWNERS](OWNERS) lists the reviewers. Contributions use [Apache 2.0](LICENSE).
