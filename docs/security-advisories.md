# File Browser advisory disposition

This inventory records how ZenFM treats the published advisories from the
upstream File Browser repository as of 2026-08-08. `Removed` means the affected
feature does not exist in ZenFM. `Control` means the replacement design and its
regression tests must remain in place.

| Advisory | Disposition | ZenFM control |
| --- | --- | --- |
| GHSA-v3jv-rmh2-635j | Removed | No JWT or proxy authentication. |
| GHSA-77x8-73f4-5485 | Removed | No per-user descendant rules; rooted operations still receive traversal tests. |
| GHSA-7whw-q6gh-xr59 | Removed | No per-user download permission; checksums still require owner/share authorization. |
| GHSA-m9f5-2232-frp6 | Control | Upload cleanup uses rooted paths and never follows symlinks. |
| GHSA-fgm5-pw99-w2p7 | Removed | No access-rule engine; all API paths are canonicalized platform-independently. |
| GHSA-576v-w77m-gr84 | Removed | No signup or multiple usernames. |
| GHSA-j7jh-37pf-mf8h | Removed | No proxy/hook provisioning. |
| GHSA-ffv3-7h97-993q | Control | Declared upload length and disk limits are enforced for every chunk. |
| GHSA-833g-cqhp-h72j | Control | Share responses never serialize password or capability hashes. |
| GHSA-83xp-526h-j3ww | Control | Archive names reject backslashes, absolute paths, and traversal. |
| GHSA-7rc3-g7h6-22m7 | Removed | One fixed owner record. |
| GHSA-6759-996p-gpj6 | Removed | No signup. |
| GHSA-8wc8-hf36-mjh9 | Control | Rooted writes reject symlink traversal. |
| GHSA-pp88-jhwj-5qh5 | Control | Exact and descendant shares are invalidated on rename/delete. |
| GHSA-fmm7-x4gx-8jhr | Control | Failed-upload cleanup uses the rooted filesystem API. |
| GHSA-gxjx-7m74-hcq8 | Control | Archive entry paths use slash-normalized safe names. |
| GHSA-3q2p-72cj-682c | Control | Shares require an existing target and bind to its canonical path. |
| GHSA-239w-m3h6-ch8v | Control | Symlinks are metadata only and cannot be traversed through owner or public APIs. |
| GHSA-v7vv-5wj2-gfcj | Control | Password changes delete all sessions and personal tokens transactionally. |
| GHSA-8c9q-7855-wfxq | Removed | No command execution. |
| GHSA-5ww9-jg6q-38r7 | Removed | No cross-user ownership; share deletion uses exact IDs. |
| GHSA-j9jx-hp4c-ghhh | Removed | No rule rebasing; public shares are rooted capabilities. |
| GHSA-jvpw-637p-h3pw | Removed | No executable authentication hooks. |
| GHSA-m93h-4hw7-5qcm | Removed | No executable authentication hooks. |
| GHSA-7526-j432-6ppp | Removed | No proxy provisioning or execute permission. |
| GHSA-67cg-cpj7-qgc9 | Removed | No per-user download permission; file reads still require owner/share authorization. |
| GHSA-5q48-q4fm-g3m6 | Removed | No path-prefix rules; canonical rooted path tests remain. |
| GHSA-v9w4-gm2x-6rvf | Removed | No mutable user permissions; deleting a share revokes its public sessions. |
| GHSA-xfqj-3vmx-63wv | Removed | No arbitrary branding templates. |
| GHSA-5vpr-4fgw-f69h | Control | EPUB content is isolated and sanitized without script privileges. |
| GHSA-x8jc-jvqm-pm3f | Removed | No signup or execute permission. |
| GHSA-ffx7-75gc-jg7c | Removed | No post-upload hooks; negative lengths are rejected. |
| GHSA-5gg9-5g7w-hm73 | Removed | No signup or admin role. |
| GHSA-9f3r-2vgw-m8xp | Control | Move/copy destinations are canonicalized within the root. |
| GHSA-xqp3-jq6g-x3qm | Removed | No proxy authentication. |
| GHSA-79pf-vx4x-7jmm | Control | Resumable-upload deletion requires owner authentication and CSRF. |
| GHSA-68j5-4m99-w9w9 | Control | Public operations are limited to an active share capability. |
| GHSA-mr74-928f-rw69 | Control | Public paths are resolved beneath the shared target. |
| GHSA-hxw8-4h9j-hq2r | Control | Password changes require a browser session and CSRF token. |
| GHSA-4mh3-h929-w968 | Control | Repeated leading slashes and encoded separators are rejected. |
| GHSA-43mm-m3h2-3prc | Control | One dummy Argon2id verification and uniform responses prevent username timing distinction. |
| GHSA-6jqf-mv7m-3q7p | Control | Supported Go HTTP stack and dependency vulnerability scanning are release gates. |
| GHSA-6cqf-cfhv-659g | Control | Share deletion addresses an exact opaque share ID. |
| GHSA-w5fm-68j4-fpc4 | Control | Login bodies, concurrent hashes, and attempts are bounded. |
| GHSA-7xwp-2cpp-p8r7 | Control | Opaque server-side sessions are individually revocable and never renewed by replay. |
| GHSA-7xqm-7738-642x | Control | Text, document, media, decoded-image, and rendered-image preview limits are enforced before unsafe allocation or playback. |
| GHSA-rmwh-g367-mj4x | Control | Passwords and bearer credentials are accepted only in bodies/headers. |
| GHSA-3v48-283x-f2w4 | Control | Share password success creates a scoped server-side public session. |
| GHSA-cm2r-rg7r-p7gg | Control | Argon2id hashes, bounded password input, and revocation on change. |
| GHSA-3q2w-42mv-cph4 | Removed | No command execution. |
| GHSA-hc8f-m8g5-8362 | Removed | No command execution. |
| GHSA-jj2r-455p-5gvf | Control | State, keys, sockets, and copied entries are forced to owner-only modes, even under a permissive umask. |
| GHSA-w7qc-6grj-w7r8 | Removed | No command execution. |
| GHSA-4wx8-5gm2-2j97 | Control | Markdown raw HTML is escaped, unsafe links are stripped, and the sanitized result remains under the application CSP. |

ZenFM intentionally permits the owner to read or alter regular ZenFM state and
certificate files when its settings directory lies below the configured file
root. That behavior is outside these isolation controls and is documented as an
accepted risk.
