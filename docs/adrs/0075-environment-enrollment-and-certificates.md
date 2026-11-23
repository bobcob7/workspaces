# 0075. An environment is enrolled with a one-time code that pins `ws`'s CA and buys a short-lived client certificate; certificates renew over the live connection and are revoked by `ws env rm`

- Status: Accepted (2026-11-22), amended the same day (see Amendments)
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
3. **A code is useless after one use**, and useless to
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
`CONFIG_DIR/environments/ca/` (key mode 0600, certificate **1 year**) and a
server certificate for `public_name` (1 year, regenerated when the CA
rotates). The CA key is never read by anything but the daemon. A short
life is deliberate: rotation is a routine, tested flow, not a rare one.

**The code.** `ws env add <name>` creates an environment record
(`environments/<id>.toml`: id, name, created time, state `pending`) and
prints one line:

```
ws-enroll-v1.<base64url: {"h":"ws.example.com:8884","f":"<sha256 of CA cert>","s":"<128-bit secret>"}>
```

Only the SHA-256 of the secret is stored (`secret_hash`). The code has no
expiry: it works until it's used once, or the pending environment is
removed (`ws env rm`) or re-coded (`ws env rotate`). It is printed once and
never logged.

**Enrolling.** On the remote: `ws env agent enroll <code>`.

1. The agent generates an ECDSA key and a CSR locally.
2. It connects to the host in the code over TLS and **verifies the
   server's CA by the pinned fingerprint `f`** (the server presents the
   chain; the agent checks it contains a CA whose hash is `f`, and that the
   leaf chains to it). A mismatch aborts before anything secret is sent.
3. It sends the CSR and the secret `s` once, over that connection, to the
   enrollment listener (HTTPS `POST /v1/enroll`).
4. The daemon checks the hash in constant time, marks the
   record `enrolled` atomically (single use), signs the CSR (CN = the
   environment id, 90 days, client-auth only), and returns the
   certificate and the CA.
5. The agent writes key, certificate and CA under its own
   `CONFIG_DIR/environment/` (key 0600; on Windows, an ACL for the
   user) and starts `ws env agent run`.

The enrollment listener answers nothing else.

**Renewal.** The agent sends `cert.renew` with a new CSR over the live
mTLS connection, on `env/<id>/up` (see Amendments), once its certificate is within
`[environments] renew_before_days` of expiring (default 30, an integer from
1 to 60); the controller signs it for the same CN. The agent re-checks every
hour, so a machine that was asleep renews as soon as it's back, if its
certificate hasn't expired. A certificate that expires while the agent was offline needs
a new code (`ws env rotate <name>` prints one for an existing
environment).

**Rotating the CA.** `ws env ca rotate` creates a new CA and keeps the old
one as a second trust root, so nothing disconnects:

1. The broker trusts both CAs for client certificates, and serves a server
   certificate signed by the new one.
2. The controller publishes the new CA bundle (both roots) to every
   connected agent (`ca.update`, retained until acknowledged); an agent
   stores it, and from then on verifies the broker against either root.
3. Each agent's next `cert.renew` is signed by the new CA. `ws env ca
   status` shows how many environments still hold a certificate from the
   old one.
4. `ws env ca rotate --finish` drops the old CA once none do (or `--force`,
   which disconnects the rest: they need `ws env rotate`).

New enrollment codes carry the new CA's fingerprint as soon as step 1 is
done. `ws daemon status` and Settings › Environments warn when the CA is
within `renew_before_days` of expiring, and the daemon rotates nothing by
itself.

**Revocation.** `ws env rm <name>` marks the environment revoked: the
broker refuses its certificate serial from then on, and disconnects it
now (see Amendments). A revoked or removed environment's workspaces on the remote are left
alone, and its controller snapshot is deleted. The revocation list is
a file the broker reads; it needs no CRL distribution.

**Removing from the remote.** `ws env agent unenroll` deletes the agent's
key and certificate; the controller still lists the environment as
offline until `ws env rm`.

### Why this option

1. Criterion 1: one command each side, one pasted string.
2. Criterion 2: the code carries the CA fingerprint, so the first thing the
   agent does is verify the server, as kubeadm does.
3. Criterion 3: a 128-bit secret, stored hashed, valid once; `ws env rotate` cancels an unused one.
4. Criterion 4: the key is generated on the remote and only a CSR leaves.
5. Criterion 5: renew in-band on a configurable lead time, rotate the CA with an overlap, revoke by serial.

Where it's weak: the code is a bearer secret that stays valid until used,
so someone who reads it from your clipboard or shell history can enroll a
machine first; an enrollment shows up in Settings › Environments
immediately for the user to confirm or remove, and `ws env rotate` cancels
an unused code. And 90-day certificates mean a machine switched off for
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

- New config: `[environments] renew_before_days` (default 30). New files: `environments/ca/`, `environments/<id>.toml`,
  `environments/revoked`, and the agent's `environment/` directory;
  documented in `docs/state.md`.
- New commands for people: `ws env add|list|rm|rotate`, `ws env ca rotate|status`,
  `ws env agent enroll|run|status|unenroll`; error codes
  `enrollment_failed` (exit 1), `enrollment_used` (exit 4), and the three
  more in Amendments.
- Conformance: Go tests that a code works once and not twice and that a
  wrong secret is refused; that a wrong fingerprint aborts before the
  secret is sent; that the private key never appears in any message; that a
  renewed certificate keeps its CN; that an agent renews exactly when it's
  within `renew_before_days` (with a fake clock, at the boundary and a day
  either side, and for a changed setting); that a revoked serial is refused
  and disconnected.
- CA rotation tests, with a fake clock and the embedded broker: (a) after
  `ws env ca rotate`, connected agents stay connected, receive the bundle
  and verify the broker against the new root; (b) an agent that only knows
  the old root still connects until `--finish`; (c) a renewal after rotation
  is signed by the new CA and keeps its CN; (d) `ws env ca status` counts
  environments on the old CA, and `--finish` refuses while any remain
  unless `--force`; (e) after `--finish` an old-CA certificate is refused;
  (f) a new enrollment code carries the new fingerprint, and one made before
  rotation fails the pin check cleanly; (g) a CA within `renew_before_days`
  of expiry produces the warning; (h) rotating twice in a row keeps at most
  two roots.
- **Unverified:** certificate renewal time under clock skew on a machine
  that was suspended; CA rotation with two roots on the broker (the broker
  spike didn't run it); and Windows key-file ACLs (see
  [0076](0076-environment-platforms-and-phasing.md)).

## Amendments (2026-11-22)

**`ca.update`.** The controller sends a request on `env/<id>/req`,
`{"op": "ca.update", "args": {"ca_bundle": "<PEM>"}}`, and the agent answers
on `env/<id>/res/<req>`. "Retained until acknowledged" is done without
state: whenever an agent's hello lists `ca_fingerprints` that don't include
every root the controller trusts, the controller sends `ca.update`.

**`renew_before_days` is the agent's own.** The agent reads
`[environments] renew_before_days` from its own `config.toml`, with the same
key and default (30). The controller's value drives only the CA-expiry
warning.

**Record states.** `environments/<id>.toml` has `state = "pending"` or
`"enrolled"`. Renewal records the up to 5 certificates it superseded in
`prior_serials`. `ws env rm` revokes the current certificate and those, then
deletes the record. `ws env rotate` revokes the same serials and makes the
record `pending` with a new code.

**Error codes.** Besides `enrollment_failed` and `enrollment_used`, the
code set gains `environment_unknown` (exit 3, from
[0072](0072-remote-environments.md)), `environment_exists` (exit 4: the name
is taken, or `enroll` runs on an enrolled machine) and `ca_in_use` (exit 4:
`ws env ca rotate --finish` while environments hold old-CA certificates).
`environment_offline`, `environment_outdated` and `environment_mismatch`
come with the presence work.

**`cert.renew` goes to `env/<id>/up`**, as [0074](0074-mqtt-transport-embedded-broker.md)
(amended) says.

**Revocation in the broker.** The refused agent sees only a broken pipe or
EOF at its first read, because TLS 1.3 reports the refusal late, not a TLS
error from the dial. The broker refuses the serial in the handshake, and
`Revoke` disconnects a live client with that serial. The spike recommends
that `ws env rm` also delete the persisted session.

## References

[^kubeadm]: Kubernetes, "kubeadm join", "Securing your installation even more." <https://kubernetes.io/docs/reference/setup-tools/kubeadm/kubeadm-join/> (accessed 2026-11-22). Describes `--discovery-token-ca-cert-hash` to pin the cluster CA when joining with a bootstrap token.
