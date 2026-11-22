# 0075. An environment is enrolled with a one-time code that pins `ws`'s CA and buys a short-lived client certificate; certificates renew over the live connection and are revoked by `ws env rm`

- Status: Proposed
- Date: 2026-11-22

## Context

### Problem

A new machine has no certificate, so it can't use the mutual-TLS listener
([0074](0074-mqtt-transport-embedded-broker.md)). How does the user add
one with a single copyable code, without an attacker on the network or a
leaked code being able to impersonate the user's `ws`, or enroll a machine
of their own?

### What a solution needs

1. **One step for the user**: `ws env add`, copy the output, run it on the
   remote.
2. **The agent authenticates the controller** from the first message
   (no trust-on-first-use of an unverified server).
3. **A code is useless after one use or a short time**, and useless to
   someone who only saw a log line of it.
4. **Private keys never leave the machine that made them.**
5. **Rotation and revocation** without re-enrolling everything.

### Relevant facts

- kubeadm joins nodes with a short-lived bootstrap token and a pinned
  hash of the CA's public key, so a node verifies the server before it
  presents the token[^kubeadm].
- `secrets.env` is `ws`'s only secrets file, mode 0600
  ([0066](0066-runner-security-boundaries.md)).
- Go's `crypto/x509` can create a CA, sign CSRs and issue ECDSA P-256
  client certificates with no third-party dependency.

## Decision

**The CA.** The first `ws env add` creates an ECDSA P-256 CA in
`CONFIG_DIR/environments/ca/` (key mode 0600, certificate 10 years) and a
server certificate for `public_name` (1 year, regenerated on renewal).
The CA key is never read by anything but the daemon.

**The code.** `ws env add <name>` creates an environment record
(`environments/<id>.toml`: id, name, created time, state `pending`) and
prints one line:

```
ws-enroll-v1.<base64url: {"h":"ws.example.com:8884","f":"<sha256 of CA cert>","s":"<128-bit secret>"}>
```

Only the SHA-256 of the secret is stored (`secret_hash`), with an expiry
of 15 minutes (`--ttl` up to 24 h). The code is printed once and never
logged.

**Enrolling.** On the remote: `ws env agent enroll <code>`.

1. The agent generates an ECDSA key and a CSR locally.
2. It connects to the host in the code over TLS and **verifies the
   server's CA by the pinned fingerprint `f`** (the server presents the
   chain; the agent checks it contains a CA whose hash is `f`, and that the
   leaf chains to it). A mismatch aborts before anything secret is sent.
3. It sends the CSR and the secret `s` once, over that connection, to the
   enrollment listener (HTTPS `POST /v1/enroll`).
4. The daemon checks the hash and expiry in constant time, marks the
   record `enrolled` atomically (single use), signs the CSR (CN = the
   environment id, 90 days, client-auth only), and returns the
   certificate and the CA.
5. The agent writes key, certificate and CA under its own
   `CONFIG_DIR/environment/` (key 0600; on Windows, an ACL for the
   user) and starts `ws env agent run`.

The enrollment listener answers nothing else, rate-limits (5 attempts per
minute per source, 20 per hour total; a wrong secret counts), and closes
the pending record after 5 wrong attempts.

**Renewal.** At 2/3 of a certificate's life the agent sends `cert.renew`
with a new CSR over the live mTLS connection; the controller signs it for
the same CN. A certificate that expires while the agent was offline needs
a new code (`ws env rotate <name>` prints one for an existing
environment).

**Revocation.** `ws env rm <name>` marks the environment revoked: the
broker refuses its certificate serial from then on, and disconnects it
now. A revoked or removed environment's workspaces on the remote are left
alone, and its controller snapshot is deleted. The revocation list is
a file the broker reads; it needs no CRL distribution.

**Removing from the remote.** `ws env agent unenroll` deletes the agent's
key and certificate; the controller still lists the environment as
offline until `ws env rm`.

### Why this option

1. Criterion 1: one command each side, one pasted string.
2. Criterion 2: the code carries the CA fingerprint, so the first thing the
   agent does is verify the server, as kubeadm does.
3. Criterion 3: a 128-bit secret, stored hashed, valid once, for minutes.
4. Criterion 4: the key is generated on the remote and only a CSR leaves.
5. Criterion 5: renew in-band, revoke by serial.

Where it's weak: the code is still a bearer secret for 15 minutes, so
someone who reads it from your clipboard in that window can enroll a
machine; we show the expiry and the enrolled machine's name, and an
enrollment shows up in Settings › Environments immediately for the user to
confirm or remove. And 90-day certificates mean a machine switched off for
longer needs a new code.

## Alternatives

### Pre-shared username and password on the broker

**Pros:** trivial.
**Cons:** not mutual TLS; one leaked password enrolls anything.

### Trust on first use of the broker's certificate

**Pros:** shorter code.
**Cons:** an attacker who answers first at the address gets the secret
and the machine.

### Long-lived certificates, no renewal

**Pros:** no renewal code path.
**Cons:** a lost machine stays trusted for years unless explicitly
revoked, and revocation lists grow.

## Consequences

- New config and files: `environments/ca/`, `environments/<id>.toml`,
  `environments/revoked`, and the agent's `environment/` directory;
  documented in `docs/state.md`.
- New commands for people: `ws env add|list|rm|rotate`,
  `ws env agent enroll|run|status|unenroll`; error codes
  `enrollment_failed` (exit 1), `enrollment_expired` (exit 4).
- Conformance: Go tests that a code works once and not twice; that an
  expired code and a wrong secret are refused and counted; that a wrong
  fingerprint aborts before the secret is sent; that the private key never
  appears in any message; that a renewed certificate keeps its CN; that a
  revoked serial is refused and disconnected.
- **Unverified:** certificate renewal time under clock skew on a machine
  that was suspended; and Windows key-file ACLs (see
  [0076](0076-environment-platforms-and-phasing.md)).

## References

[^kubeadm]: Kubernetes, "kubeadm join", "Securing your installation even more." <https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-join/> (accessed 2026-11-22). Describes `--discovery-token-ca-cert-hash` to pin the cluster CA when joining with a bootstrap token.
