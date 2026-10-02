# ZenFM peer protocol v1

ZenFM Send is a ZenFM-only, metadata-first transfer protocol inspired by
[LocalSend's approval and certificate-fingerprint model](https://github.com/localsend/protocol/blob/main/README.md).
It is not LocalSend interoperable. The service is disabled when explicit HTTP
mode is enabled or owner setup is incomplete.

## Discovery

Peers exchange JSON UDP datagrams of at most 2 KiB on fixed IPv4 port `54321`.
A `discover` message contains `service: "zenfm-peer"`, `version: 1`, and a
random nonce. An `announce` response echoes the nonce and adds the display
name, HTTPS port, and 64-character SHA-256 TLS public-key fingerprint. Senders
use the datagram's source address, never an advertised hostname. Self
fingerprints, malformed messages, late nonces, and more than 32 results are
ignored.

Discovery authenticates nothing by itself. Before offering, the sender pins
the selected receiver certificate. Before showing the offer, the receiver
calls the sender's `info` endpoint at the TCP source address and advertised
port and pins the sender certificate. Redirects and fingerprint changes are
rejected.

## HTTPS endpoints

All JSON metadata is bounded to 1 MiB. IDs and bearer tokens are random,
URL-safe capabilities. Offer polling and transfer routes bind the capability
to the offer's TCP source IP.

| Method and path | Purpose |
| --- | --- |
| `GET /api/peer/v1/info?challenge=…` | Return service, version, display name, fingerprint, and the echoed challenge. |
| `POST /api/peer/v1/offers` | Submit sender identity, item summary, and a complete file/directory manifest. Returns an offer ID and decision capability. |
| `GET /api/peer/v1/offers/{offerId}` | Poll `pending`, `accepted`, `declined`, or `expired`; accepted responses add a session ID and transfer capability. |
| `DELETE /api/peer/v1/offers/{offerId}` | Cancel a pending offer. |
| `PUT /api/peer/v1/transfers/{sessionId}/files/{fileId}` | Stream one declared regular file with an exact `Content-Length`. |
| `POST /api/peer/v1/transfers/{sessionId}` | Publish a fully received transfer and return its public destination. |
| `DELETE /api/peer/v1/transfers/{sessionId}` | Cancel and remove staged content. |

`Authorization: Bearer <capability>` is required after offer creation. Offers
expire after two minutes. Only one incoming and one outgoing transfer may be
active per service. An accepted stream fails after 30 seconds without
progress; v1 restarts interrupted transfers from the beginning.

## Manifest and publication rules

The manifest contains a root entry at `.` plus regular-file and directory
entries with unique IDs and slash-separated relative paths. Symlinks, special
files, pseudo-filesystems, traversal, duplicate paths, missing parents, more
than 10,000 entries, or more than 2 GiB are rejected. Directory entries have
zero size. Summary bytes and counts must exactly match the manifest.

The receiver stages content in a hidden internal directory, verifies every
declared length while streaming, and exposes nothing until completion. It then
atomically publishes within its configured peer receive root. Existing names are never
overwritten; collisions become `name (1)`, `name (2)`, and so on. Staging is
removed on decline, cancellation, timeout, interruption, restart, validation
failure, or successful publication.

## KOReader and Android boundary

The local control bridge accepts bounded `peer-discover`, `peer-send`,
`peer-accept`, `peer-decline`, `peer-cancel`, and `peer-status` commands. Its
atomic `peer-events.json` is versioned and contains display metadata and
progress only—never capabilities or filesystem source paths.

On Android the event file is shared and therefore untrusted. The companion
revalidates every command against the backend's live discovery, offer, and
rooted path state. Its overlay-resistant native dialog additionally confirms
send and accept. KOReader polls only while awake, so closed or sleeping
receivers do not present offers and those offers expire.
