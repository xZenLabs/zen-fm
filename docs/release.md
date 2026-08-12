# ZenFM release process

ZenFM publishes only the four KOReader bundles and Android companion below as
installable artifacts. Qualification evidence is non-installable release
metadata. The SPDX SBOM is generated, GitHub-attested, and retained as a
workflow artifact. Updaters accept only the five exact installable asset names
and verify the SHA-256 digests recorded by GitHub for those assets.

| Installable artifact | Contents |
| --- | --- |
| `ZenFM-koreader-ereader-<version>.zip` | ARM soft-float and hard-float backends |
| `ZenFM-koreader-linux-<version>.zip` | Linux amd64 and arm64 backends |
| `ZenFM-koreader-macos-<version>.zip` | Universal macOS amd64/arm64 backend |
| `ZenFM-koreader-android-<version>.zip` | KOReader-to-companion integration, no bundled server |
| `ZenFM-android-<version>.apk` | Android companion for `armeabi-v7a` and `arm64-v8a` |

No standalone desktop/server archives, Windows or BSD packages, Kindle
standalone package, container image, s6 image, or Homebrew package is part of a
ZenFM release.

KOReader plugin ZIP updates have application-level atomic rollback. Android APK
updates are durably journaled and health-gated across Package Installer
replacement, but recovery from an APK that cannot launch requires the owner to
install a newer signed build or manually reinstall the prior trusted APK;
ZenFM does not use privileged or hidden downgrade APIs.

## Required checks

Protect `main` and require these checks before a release tag can be created:

- `CI required checks`, which aggregates Go vet and race tests, the fuzz smoke test,
  Node 24 frontend typecheck/lint/Vitest/build and real-binary Playwright tests,
  KOReader Lua/shell/package tests, Android compile and JVM tests, source SBOM
  generation, and a real build/layout validation of all four KOReader bundles
  plus both-ABI APK. Pull-request packages are deliberately unsigned
  development artifacts and are never published.
- `Security required checks`, which aggregates `govulncheck`, OSV Scanner, npm
  audit, and the pull-request dependency review when applicable.
- CodeQL `Analyze go`, `Analyze javascript-typescript`, and
  `Analyze java-kotlin` checks. CodeQL uses the v4 action generation.

The stable-release workflow re-queries those exact checks on the tag commit. A
green result on some later commit does not qualify an older tag.

## Legacy compiler qualification

The e-reader ARM binaries are compiled from the source release pinned in
`toolchains/legacy/VERSION` and `SHA256`. It must remain `go1.26.5`; the
bootstrap verifies the official source archive before applying the reviewed
Linux/ARM `epoll_wait` compatibility patch. Stable builds bootstrap into a
fresh runner directory and never substitute an unpatched or end-of-life Go
1.19/1.20 compiler.

The compiler patch is necessary, but it is not hardware evidence. Before a
stable release, run `Old-kernel QEMU qualification` on the protected
`zenfm-old-kernel-qemu` self-hosted runner. That runner must expose an
executable `ZENFM_OLD_KERNEL_QEMU_HARNESS` and a genuine Kindle-era Linux 2.6
QEMU image. The harness must boot and exercise both ARM float ABIs and write the
`zenfm-old-kernel-qemu-v1` JSON contract checked by
`scripts/run-old-kernel-qemu-smoke.sh`.

Both qualification runner classes must be dedicated and ephemeral (or restored
to a known-clean snapshot after each job). Keep their harness executable
outside the checkout, owned by the runner administrator, and never place
release-signing secrets on them. The environment reviewer must inspect the tag
commit before allowing repository code to execute on a device-connected host.
Runner images must provide `jq`, `patch`, `tar`, and either `curl` or `wget`;
the setup actions supply the pinned Go and Node host toolchains. Keep the
self-hosted Actions runner current enough for the commit-pinned Node 24 action
runtime.

## Physical Kindle attestations

Run `Physical Kindle qualification` three times against the same stable tag:

1. `kindle4`
2. `kindle5`
3. `paperwhite1`

These jobs run only on the protected `zenfm-physical-kindle` self-hosted runner.
The runner must have the selected physical device attached and provide
`ZENFM_PHYSICAL_KINDLE_HARNESS`. The external harness performs start, login,
browse, upload, download, TLS-fingerprint, and stop checks, and records only a
one-way serial hash—not a device serial. The workflow rejects incomplete JSON,
then uses GitHub OIDC to attest the evidence file and uploads it with the exact
tested commit in the artifact name.

A person clicking a workflow button is not a smoke-test result. Missing
runners, harnesses, devices, JSON checks, or GitHub attestations fail closed.
The stable-release workflow requires the successful QEMU run ID and all three
physical run IDs, verifies their workflow identity, protected default-branch
origin, commit, evidence schema, and GitHub provenance, and publishes the
records alongside the release. Each record contains the independently computed
SHA-256 digests of both legacy ARM backends. All four records must agree, each
physical harness must identify one of those exact binaries as the binary it
tested, and packaging fails unless the release's hard- and soft-float binaries
match those digests byte-for-byte.

## Signing configuration

Create two protected GitHub environments:

- `stable-release-qualification` protects the QEMU and hardware runner jobs.
  Require trusted reviewers and restrict deployment branches to `main`.
- `stable-release` protects access to the Android release key and publication.
  Require independent reviewers, restrict it to `main`, and prevent
  administrators from bypassing the rule where repository policy permits.

Store these values as environment secrets on `stable-release`:

| Secret | Requirement |
| --- | --- |
| `ANDROID_KEYSTORE_BASE64` | Base64 of the Android release keystore |
| `ANDROID_KEYSTORE_PASSWORD` | Keystore password |

Generate the keystore outside GitHub. The alias must be `zenfm-release` because
the build fixes that non-secret value in Gradle:

```sh
keytool -genkeypair \
  -keystore zenfm-release.p12 \
  -storetype PKCS12 \
  -alias zenfm-release \
  -keyalg RSA \
  -keysize 4096 \
  -sigalg SHA256withRSA \
  -validity 10000
```

Use the prompted password for `ANDROID_KEYSTORE_PASSWORD`. PKCS#12 uses that
same password for its private-key entry. Generate the remaining secret value as
one line:

```sh
openssl base64 -A -in zenfm-release.p12
```

Store that output as `ANDROID_KEYSTORE_BASE64`. Keep an offline backup of the
keystore and password. Android requires every update to use the same signing
key, so losing or replacing it prevents upgrades of existing installations.

If a JKS keystore was already generated, preserve its key by converting it
rather than generating another one:

```sh
keytool -importkeystore \
  -srckeystore zenfm-release.jks \
  -destkeystore zenfm-release.p12 \
  -deststoretype PKCS12 \
  -srcalias zenfm-release \
  -destalias zenfm-release
```

Then base64-encode `zenfm-release.p12` as above. Use the destination password as
`ANDROID_KEYSTORE_PASSWORD`.

## Stable promotion

1. Set `VERSION` to a stable `major.minor.patch`, pass every required check,
   and create the matching existing tag `v<version>`.
2. Run the QEMU qualification and the three physical-device qualifications for
   that tag by dispatching each workflow from `main` and supplying the tag as
   `release_ref`. Record their GitHub Actions run IDs.
3. Dispatch `Stable release` from `main` with the tag and four run IDs.
4. Approve the `stable-release` environment only after reviewing the evidence
   summaries and expected source commit.
5. Download the published assets, verify GitHub artifact attestations, and
   install-test the final archives before announcement.

The workflow will not manufacture or waive old-kernel or physical-device
evidence. Pre-releases need a separate explicitly documented workflow if the
project later chooses to publish them; the stable workflow rejects prerelease
versions.
