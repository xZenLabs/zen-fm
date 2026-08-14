# Security Policy

ZenFM is a network-facing file manager. Only the latest release receives
security fixes; fixes are not backported.

Report vulnerabilities privately through the GitHub repository's **Security >
Report a vulnerability** form. Include the affected version, reproduction
steps, impact, and a plaintext proof of concept when possible. Do not open a
public issue before a fix is available.

## Deliberate high-risk modes

ZenFM starts with HTTPS and a platform storage directory. Two settings weaken
that posture and are deliberately explicit:

- **HTTP fallback** exposes credentials, sessions, and files to observers on
  the network. It is never enabled automatically.
- **Advanced root mode** serves `/`. It displays ZenFM's database, logs,
  executable, and TLS material and permits normal operations on those regular
  files. The owner can disclose credentials, corrupt the database, delete the
  certificate, or lock themselves out. Special files and pseudo-filesystems
  are listed, but are not opened as regular content.

The first-run password is public (`koreader123456789`) and exists only to enter
the mandatory password-change flow. Start a new device on
a trusted network and change it immediately.

## Security boundaries

- ZenFM has one owner. There are no secondary users, roles, signup, proxy
  authentication, no-auth mode, executable hooks, or command runner.
- Browser sessions are opaque, revocable, server-side records. Personal API
  tokens are separate and cannot modify account or lifecycle settings.
- The KOReader control socket is local and mode `0600`. The LAN API cannot stop
  or update the process.
- Public shares are read-only capabilities bounded to their configured path
  and expiry.

The upstream File Browser advisory disposition is maintained in
[`docs/security-advisories.md`](docs/security-advisories.md).
