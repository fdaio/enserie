# Security

## The token is the whole security model

There is no account, no control plane, and no key distribution. Two nodes trust
each other because the operator pasted a token from one screen to the other,
and the token carries a 16-byte shared secret in the clear.

Treat a token like a password:

```bash
sudo ens accept TOKEN
```

Anyone who reads that token holds the secret and can impersonate the accepting
node. `docs/protocol.md` describes the fields and which side checks what.

## What is protected

Overlay packets are inside TLS 1.3 on both paths. Direct QUIC pins the peer
certificate fingerprint; a relayed connection is wrapped in TLS right after the
relay splices it. The relay copies ciphertext and never sees a packet body.

Certificate pinning means a network observer cannot present another
certificate, and no certificate authority is involved.

## What is not protected

**A token is a bearer credential for 30 minutes.** `Encode` sets an expiry and
`ParseInvite` refuses a token past it, including one with no expiry at all. The
offering node pairs one peer and refuses a different peer id afterwards.

**A token can still be replayed inside its window.** Expiry bounds how long a
leaked token is worth, not who may use it. Only the offering node can enforce
single use, and only for peers that arrive after it has paired.

**A token holder can impersonate the accepting node.** The offering node cannot
pin the accepting node's certificate, because the fingerprint does not exist
until the connection does. It accepts any non-empty fingerprint and
authenticates on the shared secret alone.

**The relay sees connection metadata.** It cannot read packets, but it knows
both IP addresses, when the connection was made, how many bytes crossed it, and
for how long. It can also drop or delay the connection.

**A compromised relay can refuse to pair two nodes.** Nothing detects a missing
relay, and the operator has no way to tell a blocked pair from a broken network.

**Certificates are ephemeral and cannot be revoked.** The CLI writes a fresh
self-signed certificate to a temporary directory on every start, so a restart
produces a new fingerprint. There is no revocation list and no rotation
schedule.

**Traffic size and timing are visible to the network.** TLS hides the contents,
not the envelope.

## Reporting a vulnerability

Report it privately to the maintainers rather than in a public issue. Include
what an attacker can reach, what they need in order to do it, and how to
reproduce it. Please do not test against the hosted relays
(`relay-1.getfda.dev`, `relay-2.getfda.dev`) or against infrastructure you do
not own.

## Hardening notes for operators

- Run `ens invite` on a host you trust the operator of. The inviting host
  accepts whichever peer presents the secret.
- Both nodes need root or `CAP_NET_ADMIN` to open the TUN, so the process is
  privileged. Only run the binary you built or fetched from a release you
  checked.
- Each release publishes `SHA256SUMS`. Verify it after downloading:

  ```bash
  shasum -a 256 -c SHA256SUMS
  ```

  macOS binaries carry an ad-hoc signature, which is not a notarised one and
  stops being meaningful on any machine other than the one that produced it.
- Prefer a relay you operate when the path matters. Point the overlay range
  somewhere no other network claims, which is what `ens invite --subnet` is
  for.
