# First run

On first start the server has no household. It stays in setup mode until you finish the setup wizard. Until then every API call except setup returns `503 setup_required`.

## Get the setup link

The server prints a one-time setup link in its log:

```text
Setup required: open http://<host>:8080/setup?token=<token>
```

The base address is `CHORECAST_PUBLIC_URL`, or `http://localhost:8080` when it isn't set. Find the newest link:

```sh
docker compose -f deploy/compose.yaml logs chorecast | grep "Setup required"
```

The token changes every time the server starts, until setup finishes. Use the last link in the log. An old link returns `403 invalid_setup_token`. Once setup completes, the token is deleted and the setup endpoints answer `409 setup_complete`.

## Run the setup wizard

Open the link in a browser. The wizard creates the household, the first owner and any kids in one step.

1. **Household.** Name, timezone, day end (`HH:MM`, default `00:00`) and week start (default Monday).
2. **Owner.** Display name, username (3–32 lowercase letters, digits, `.`, `_` or `-`) and password (8 to 128 characters).
3. **Kids (optional).** Up to 20 kids: display name, birth date, color, and whether a PIN is required on shared devices.

When setup finishes, the browser is signed in as the owner. You can turn on two-factor sign-in and link SSO from the account page. For SSO, see [Single sign-on](sso.md).

## Pair a kid's phone

Each kid's own phone is paired with a QR code. Pairing is done by a parent.

1. In the web admin, open **Devices** and choose **Pair a device**.
2. Choose the kid's name.
3. Scan the QR code with the Chorecast Android app, or enter the server address and the code shown.

The code can be used once and expires after 5 minutes. A new code is needed for each device. The phone then holds a device token in its secure storage.

## Set up a shared family tablet

A shared tablet is paired the same way, but choose **Shared family device**. It shows a profile picker. Kids switch between profiles on the tablet.

- **Kid PINs.** Each kid can have a PIN, set in the kid's profile. If the kid has a PIN, the tablet asks for it before opening that profile. Five wrong PINs lock that kid on the tablet for 15 minutes.
- **Parent PIN.** A parent who has a PIN can use the tablet's parent mode for 2 minutes at a time. This gives parent actions without a full parent session. Five wrong PINs lock parent mode on that tablet for 15 minutes.
- **Lock.** The tablet returns to the picker after 2 minutes without a touch. A kid session also ends after 8 hours.

## Install the Android app

The app is a signed APK. The Google Play listing is planned for after v1, so for now install from GitHub Releases.

### Flavors

Each Android release has two APKs, attached to the `android-vX.Y.Z` release on GitHub:

| File | Flavor | Notes |
|---|---|---|
| `chorecast-play-X.Y.Z.apk` | `play` | Can use Google services, such as FCM, as a push fallback. |
| `chorecast-foss-X.Y.Z.apk` | `foss` | No Google services. It uses UnifiedPush for push. |

Both flavors have the same application ID, `dev.voidrot.chorecast`, so only one can be installed at a time. Installing the other flavor updates the app in place, because both are signed with the same project key.

### From GitHub Releases

1. Open the release page for the version you want.
2. Download the APK for your flavor.
3. Open the file on the phone and allow installs from that source when Android asks.

To check the signature on a computer, run `apksigner verify --print-certs <file>` from the Android build tools.

### With Obtainium

Obtainium can track GitHub releases and install updates from them.

1. Add the Chorecast GitHub repository as a source.
2. If a release has both APKs, add a filter with **Filter APKs by Regular Expression**. Use `foss` or `play` to pick a flavor. Without a filter, Obtainium asks you to pick an APK on each update.
3. Add the app in Obtainium using the GitHub repository URL. For the source options, see [Obtainium's documentation](https://obtainium.imranr.dev/).

## Next steps

- [Single sign-on](sso.md), to sign parents in with your identity provider.
- [Backups and restore](backups.md), before you change anything important.
- [Upgrading](upgrading.md), when a new version comes out.
