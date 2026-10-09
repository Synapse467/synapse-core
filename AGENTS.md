# synapse-core working rules

- This repository is the single definition of every hashed or signed format. `SPEC.md` and the code change together.
- Use Go. Keep dependencies to the standard library and the Stellar Go SDK; ask before adding another.
- Anything that is hashed goes through `canonical`. Never hash a struct's default JSON encoding.
- Every signature is domain separated (`identity.Sign`). Never sign a bare payload.
- Decision functions (`license.Check`) are pure and fail closed: anything not positively confirmed is a denial.
- Treat input as hostile: strict decoding (reject unknown fields), size limits before parsing, no panics on malformed input.
- Never log or return matched values, questions, answers or full documents. Usage logs hold hashes.
- Distinguish prevention (a check that refuses) from audit (evidence after the fact) in code comments and docs.
- Changing what is hashed or signed means a new format version, never an edit in place.
- Add fixture or table tests for every supported shape and for malformed input, including each way a request is denied.
- Keep `docs/REPOSITORIES.md` accurate; do not copy engine or CLI behaviour into this repository.
- Do not put credentials, private keys, seeds, tokens or real customer data in this workspace. `.env` and `identity.json` are ignored.
