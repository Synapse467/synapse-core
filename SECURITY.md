# Security

## Reporting a vulnerability

Please report vulnerabilities privately, through GitHub's **Report a vulnerability** button on this repository's Security tab. Do not open a public issue for something exploitable, and do not include real keys, tokens or customer data in a report.

You will get an acknowledgement, and we will agree a disclosure date with you once the fix is ready.

## What counts

- A way to make a capsule, license, revocation, signed request or usage log verify after it was altered.
- Two different values with the same canonical form (a hash collision at the format level).
- A signature valid in one domain that is accepted in another.
- A way to bypass `license.Check`, or a case where it allows a request it should deny.
- A parser that panics, hangs or uses unbounded memory on a crafted file.
- Anything that causes the identity file to be overwritten, exposed or created with loose permissions.

## What does not

- Licenses checked on the licensee's own machine are cooperative by design. Someone who holds a capsule file can ignore its license terms; use the gateway (`synapse serve`) when limits must be enforced. This is documented, not a vulnerability.
- Anything that needs the attacker to already hold the victim's `identity.json`.
- Testnet being reset.

## Handling secrets

Synapse identities are Stellar secret keys stored in `identity.json` with owner-only permissions on Linux and macOS (on Windows, the permissions of your user profile folder apply). Nothing in these repositories should contain a key, seed, token or real customer data; `.env` files and `identity.json` are git-ignored. If you find one committed, report it as a vulnerability.
