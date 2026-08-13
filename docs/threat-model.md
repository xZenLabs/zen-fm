# Threat model

## Assets

- Files reachable below the configured root.
- Owner password hash, session and API-token hashes, share capabilities, and
  TLS private key.
- Integrity of the ZenFM binary, KOReader plugin, updater, and Android APK.
- Device availability, storage, battery, and network service.

## Trust boundaries

- The LAN listener is untrusted. Every non-public API operation requires an
  authenticated owner session or limited personal token.
- The embedded browser application is same-origin but processes hostile file
  names and hostile file contents.
- Public-share visitors are anonymous and confined to one share capability.
- The KOReader control socket and Android control secret are device-local
  lifecycle boundaries.
- Release downloads and redirects are hostile until trusted-host, size, and
  GitHub-recorded SHA-256 verification succeeds.

## Primary threats and controls

| Threat | Controls |
| --- | --- |
| Credential theft | HTTPS default, HttpOnly cookies, no browser bearer storage, redacted logs. |
| Session replay | Random hashed server-side sessions, idle/absolute expiry, transactional revocation. |
| CSRF/cross-origin control | SameSite cookie, session-bound CSRF header, strict origin checks, no permissive CORS. |
| Password guessing/DoS | Uniform Argon2id work, request limits, per-IP/account rate limits, bounded hash concurrency. |
| Path or symlink escape | Canonical rooted operations, encoded-separator rejection, no symlink traversal. |
| Malicious file content | MIME isolation, CSP, sanitization, sandboxed rich previews, size/dimension limits. |
| Upload/archive exhaustion | Streaming I/O, declared-length enforcement, quotas, entry/count/depth limits, cancellation. |
| Share escalation | High-entropy hashed capabilities, body-based password exchange, scoped public sessions, expiry. |
| Local lifecycle abuse | Mode-0700 native runtime directory plus mode-0600 control socket, exact process identity checks, and an Android pairing secret established only after a native, overlay-resistant first-start confirmation. Sensitive Android commands require confirmation because KOReader state can reside on shared storage. |
| Malicious update | Trusted GitHub release URLs, bounded downloads, redirect validation, and GitHub-recorded SHA-256. Plugin trees activate atomically with rollback; APK replacements are journaled, package/version/signature revalidated, and health-gated through Android's Package Installer. |

## Accepted risks

- The setup username and password are public until the owner completes the
  mandatory password change. A hostile peer can race the legitimate owner on
  first start.
- Explicit HTTP mode provides no confidentiality or peer authentication.
- Advanced root mode intentionally exposes all regular files permitted by the
  operating system, including ZenFM state, password hashes, tokens, logs,
  executable, and TLS keys. Editing or deleting them can disclose secrets,
  corrupt state, or stop the service.
- The server is deliberately detached from KOReader and can remain running
  after KOReader exits or Wi-Fi changes until manually stopped or auto-stop is
  enabled.
- Android can stop a `specialUse` foreground service under resource or
  background policy. An ordinary sideloaded app also cannot silently downgrade
  itself: a replacement APK that cannot launch requires an owner-approved newer
  signed install or manual reinstall of the prior trusted APK.
- Plugin updates trust GitHub's release metadata and asset digests. Compromise
  of the repository's release publication authority could therefore publish a
  malicious plugin ZIP. APK replacement additionally requires the persistent
  Android signing key.

## Release criteria

- All applicable upstream advisory regressions pass.
- No known unaccepted critical/high dependency or code-scanning finding.
- Automated race, unit, browser, plugin, and package-layout checks pass.
- Manual old-kernel QEMU and physical Kindle 4/5 and PW1 checks pass.
- Release bundles and the APK have GitHub-recorded SHA-256 digests and build
  provenance; the APK is signed with the persistent Android release key.
