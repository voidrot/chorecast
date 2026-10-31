# Releasing

Releases are cut by [release-please](https://github.com/googleapis/release-please) from Conventional Commits
on `main` (`.github/workflows/release.yml`, config in `release-please-config.json`).

## How it works

- release-please keeps one release PR per component (`separate-pull-requests`): **server** (with **web** linked
  to the same version, since the admin is embedded in the server binary) and **android**. Merging a release PR
  tags it (`server-v1.2.3`, `android-v1.2.3`) and creates the GitHub release.
- `feat:` bumps minor, `fix:` bumps patch, `!` / `BREAKING CHANGE:` bumps major (minor while < 1.0).
- **Server release** → multi-arch images (amd64, arm64) on GHCR:

  | Image | Tags | Contents |
  |---|---|---|
  | `ghcr.io/voidrot/chorecast` | `1.2.3`, `1.2`, `latest` | distroless, static binary (default) |
  | `ghcr.io/voidrot/chorecast` | `1.2.3-media`, `1.2-media`, `latest-media` | Alpine + ffmpeg for video transcoding |

  Both run as uid 65532, so switching variants keeps the `/data` volume readable.
- **Android release** → signed `chorecast-play-1.2.3.apk` and `chorecast-foss-1.2.3.apk` attached to the
  `android-v1.2.3` GitHub release (signatures checked with `apksigner` first). `versionName` is bumped by
  release-please in `android/app/build.gradle.kts`; `versionCode` is derived from it
  (`major·1 000 000 + minor·1 000 + patch`). A Play bundle (`bundlePlayRelease`) is built for later Play uploads.

## One-time setup: Android signing key

The signing key must stay the same for the app's whole life: Android refuses updates signed by a different key.
Keep a backup of the keystore and passwords outside GitHub.

```sh
keytool -genkeypair -v \
  -keystore chorecast-release.jks -alias chorecast \
  -keyalg RSA -keysize 4096 -validity 10000 \
  -dname "CN=Chorecast"
base64 -w0 chorecast-release.jks > chorecast-release.jks.b64   # macOS: base64 -i chorecast-release.jks
```

Add these repository secrets (Settings → Secrets and variables → Actions):

| Secret | Value |
|---|---|
| `ANDROID_KEYSTORE_BASE64` | contents of `chorecast-release.jks.b64` |
| `ANDROID_KEYSTORE_PASSWORD` | keystore password |
| `ANDROID_KEY_ALIAS` | `chorecast` (or the alias you chose) |
| `ANDROID_KEY_PASSWORD` | key password |

The Android release job fails with a clear error if the keystore secret is missing; nothing unsigned is
published.

## Local builds

- `mise run build:android:release` builds release APKs. They are **unsigned** unless these are set:
  `CHORECAST_KEYSTORE_PATH`, `CHORECAST_KEYSTORE_PASSWORD`, `CHORECAST_KEY_ALIAS`, `CHORECAST_KEY_PASSWORD`.
  CI builds the unsigned release on every PR to catch R8/minification problems early.
- `mise run build:image` builds both image variants locally (needs Docker buildx).
- Debug APKs from every CI run are attached to the run as the `chorecast-debug-apks` artifact (14 days).

## Before the first Google Play release (post-v1)

Families policy review, data-safety form, privacy policy, Play listing; upload the `play` flavor bundle. See
`docs/roadmap.md` Phase 5.
