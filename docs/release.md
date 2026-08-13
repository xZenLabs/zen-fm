# ZenFM release process

ZenFM publishes only the four KOReader bundles and Android companion below as
installable artifacts. Updaters accept only the five exact installable asset
names and verify the SHA-256 digests recorded by GitHub for those assets.

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
  KOReader Lua/shell/package tests, Android compile and JVM tests, and a real
  build/layout validation of all four KOReader bundles plus both-ABI APK.
  Pull-request packages are deliberately unsigned development artifacts and
  are never published.
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

The compiler patch is necessary, but it does not prove compatibility with an
old kernel. Before a stable release, manually run the QEMU harness against both
ARM float ABIs using a genuine Kindle-era Linux 2.6 image. The helper
`scripts/run-old-kernel-qemu-smoke.sh` validates the harness result.

## Physical Kindle qualification

Manually smoke-test the same stable tag on:

1. `kindle4`
2. `kindle5`
3. `paperwhite1`

Use `scripts/run-physical-kindle-smoke.sh` with the selected device and attached
hardware. The external harness performs start, login, browse, upload, download,
TLS-fingerprint, and stop checks. Review the results before manually dispatching
the stable-release workflow; GitHub Actions does not enforce these manual
qualification checks.

## Signing configuration

Create a protected `stable-release` GitHub environment. It protects access to
the Android release key and publication. Require independent reviewers,
restrict it to `main`, and prevent administrators from bypassing the rule where
repository policy permits.

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
2. Manually run the old-kernel QEMU qualification and the three physical-device
   qualifications against that exact tag.
3. Dispatch `Stable release` from `main` with the tag.
4. Approve the `stable-release` environment only after reviewing the manual
   test results and expected source commit.
5. Download the published assets, verify GitHub artifact attestations, and
   install-test the final archives before announcement.

The stable workflow rejects prerelease versions. It verifies the tag and
required GitHub checks, but manual QEMU and device qualification remains the
release owner's responsibility.
