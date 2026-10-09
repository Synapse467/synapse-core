# Changelog

All notable changes are recorded here. This project follows semantic versioning once it is tagged.

## [Unreleased] 0.1.0

A ground-up redesign around the idea that Synapse should need no setup.

- Capsule format `synapse.capsule/1`: one signed, versioned, citable file, with items of five kinds, an evaluation record and a policy.
- Canonical JSON with cross-language test vectors; domain-separated signatures; Stellar addresses as the only identity.
- Licenses, revocations and signed requests as files, with a pure, fail-closed decision function and a replay guard.
- A hash-chained usage log that stores question hashes only, and batch sealing.
- Optional Stellar client with built-in Testnet defaults, automatic funding and automatic restoration of expired entries.
- Written specification (`SPEC.md`).
- Removed: the hosted API, database, Redis, object storage, accounts and all environment configuration of earlier versions.
