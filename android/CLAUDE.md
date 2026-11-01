# CLAUDE.md — android

This file extends the root `CLAUDE.md` (repo-wide commands and PR rules live there; don't repeat them).

## Commands

Use `mise run <task>` from the repo root, or `./gradlew` from `android/`. mise pins JDK 21 (Gradle runs on it; bytecode targets 17).

```sh
mise run android:sdk            # one-time: prompts to accept SDK licences, installs platform-tools, platforms;android-37.2, build-tools;37.0.0
mise run build:android          # ./gradlew assembleDebug
mise run build:android:release  # unsigned unless CHORECAST_KEYSTORE_* are set
mise run lint:android           # ./gradlew lint
mise run test:android           # :core:test, testFossDebugUnitTest, testPlayDebugUnitTest
```

CI's `android` job runs the same tasks with the same SDK packages (`.github/workflows/ci.yml`), plus the unsigned release build that catches R8 failures.

Run one test class or method from `android/`:

```sh
./gradlew :core:test --tests 'dev.voidrot.chorecast.core.DevicesTest'
./gradlew :core:test --tests 'dev.voidrot.chorecast.core.DevicesTest.parsesPairingQr'
./gradlew :app:testFossDebugUnitTest --tests 'dev.voidrot.chorecast.SmokeTest'
```

There is no plain `testDebugUnitTest`: each flavor has its own unit-test task (see `mise.toml`), so app tests need the flavor in the task name.

Most tests are in `core/src/test`, which is plain JVM and needs no emulator. `app/src/test` holds only `SmokeTest`. Put logic under test in `core`.

## Modules

- `core`: Kotlin/JVM with no `android.*` imports. Server URL parsing (`ServerUrl`), pairing QR parsing, API compatibility (`Compatibility`), the API clients (`ServerChecker`, `ParentAuth`, `DeviceClient`, `HouseholdInfo`), PKCE, `ApiClients.config`, and the `TokenStore` interface. Code that can run without Android belongs here.
- `api-client`: the Kotlin client, generated at build time by `openApiGenerate` from `api/openapi-3.0.json` (the 3.0 copy, not `openapi.json`). Nothing generated is committed. After an API change, run `mise run gen:openapi` and commit the spec; CI fails on a stale spec. Build clients with `ApiClients.config`, because the generated client registers no JSON converter.
- `app`: Compose UI, Keystore, SharedPreferences, the CameraX QR scanner and the OkHttp engine. Only this module depends on Android.

The full module and UI description is in `docs/architecture.md`, "Android app structure".

## Flavors and versions

- `play` and `foss` (dimension `distribution`) have no flavor source sets or flavor-specific dependencies yet, so they build the same code. The comment in `app/build.gradle.kts` gives the intent: `play` may add FCM as a push fallback, and `foss` stays Google-free (UnifiedPush). Keep Google-only dependencies out of `main`.
- `versionName` is the literal `0.0.0` on the line marked `// x-release-please-version`. release-please rewrites it through the `android` entry in `release-please-config.json`, so don't edit it by hand.
- `versionCode` is derived from `versionName` as `major * 1_000_000 + minor * 1_000 + patch`. `minor` and `patch` must be below 1000.

## Signing and R8

- A release is signed only when `CHORECAST_KEYSTORE_PATH` is set. The other three (`CHORECAST_KEYSTORE_PASSWORD`, `CHORECAST_KEY_ALIAS`, `CHORECAST_KEY_PASSWORD`) are then read with `get()`, so a partial set fails configuration rather than producing an unsigned build. The keystore setup and the GitHub secret names are in `docs/releasing.md`.
- Release builds minify with `proguard-android-optimize.txt` and `app/proguard-rules.pro`. Add keep rules there. The unsigned release build in CI is the check.

## App structure

- DI: `ChorecastApp` creates one `AppContainer` (settings, token store, OkHttp engine, clients). `ChorecastNavHost` builds ViewModels in `viewModel { }` factories from the container. There is no DI framework in the version catalog.
- Navigation: Navigation Compose with `@Serializable` routes in `ui/Routes.kt`. `nav.reset(...)` clears the back stack for startup, sign-in and sign-out.
- Startup (`ui/startup/StartupViewModel.kt`): with no server or mode it goes to `WelcomeRoute`. Otherwise it checks reachability and `apiVersion` through `ServerChecker`. A `DEVICE` install goes to the device screen, which checks its own pairing. A `PARENT` install calls `ParentAuth.check`, and an invalid session clears the token and goes to login.
- UI packages under `ui/`: `startup`, `welcome`, `pair` (QR or typed code), `connect` (server URL; warns on `http://`), `login`, `home`, `devices` (a parent's paired devices), `device` (kid home and the shared-tablet profile picker), `theme`. Screens take `StateFlow` state from ViewModels. All UI text is in `res/values/strings.xml`.

## Pairing and credentials

- Pairing takes a scanned `{"server","code"}` payload (`PairingQr.parse`) or a typed server and code, then calls `DeviceClient.pair`. On success `PairViewModel` writes the token to `TokenStore`, saves `serverUrl`, and sets `mode = DEVICE`.
- Parent sign-in writes its token to the same `TokenStore`. `ConnectViewModel` clears the token when the server or mode changes.
- `KeystoreTokenStore` keeps the token in the `chorecast` SharedPreferences under `api_token`, as `base64(iv):base64(ciphertext)`. It uses AES-256-GCM with a key in the Android Keystore (alias `chorecast_token_key`). If decryption fails, it clears the token and the device must pair again. `allowBackup` is off.
- `serverUrl` and `mode` are not secret and live in `SettingsStore`, in the same prefs file.
- SSO returns through `chorecast://auth` to `MainActivity` (`singleTask`). `SsoBridge` holds the PKCE verifier in memory, so a process death restarts the attempt.
- Device sessions, the idle lock and PIN lockout are described in `docs/architecture.md`, "Device pairing".

## Test conventions (`core/src/test/kotlin`)

- One `<Name>Test.kt` per main file (`DevicesTest` for `Devices.kt`). Tests use JUnit 4 (`org.junit.Test`, `org.junit.Assert`).
- HTTP is faked by passing a Ktor `MockEngine` into the client (`DeviceClient(engine)`). Tests check the request URL and `Authorization` header and the decoded result.
- Suspend code runs in `runTest`. Use `InMemoryTokenStore` in place of the Keystore store.
- Error responses map to typed results (`PairResult.InvalidCode`, `SessionResult.Locked`). Assert on those results.

## Docs that govern Android

- `docs/architecture.md`: "Android app structure", "Device pairing", and the compatibility policy.
- `docs/releasing.md`: "Android release", signing key setup, and "Local builds".
