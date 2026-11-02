# Install Chorecast

Chorecast runs as one container. It serves the API, the web admin and the background jobs, and keeps its data in one SQLite file. This guide starts it with Docker Compose.

## Requirements

- Docker with Compose v2.
- An amd64 or arm64 host. The images are published for `linux/amd64` and `linux/arm64` only.
- For a Raspberry Pi, see [Raspberry Pi notes](#raspberry-pi-notes).

## Start the server

1. Get the compose file. Clone the repository, or copy `deploy/compose.yaml` to your host.
2. Edit the file:
   - Set `TZ` to your timezone. The default is `America/New_York`.
   - If you reach the server at a fixed address, uncomment `CHORECAST_PUBLIC_URL` and set it, for example `https://chores.example.com`. The setup link and the device pairing QR code use this address.
3. Start the container:

   ```sh
   docker compose -f deploy/compose.yaml up -d
   ```

4. Find the setup link in the log. Open it to finish setup. See [First run](first-run.md).

   ```sh
   docker compose -f deploy/compose.yaml logs chorecast | grep "Setup required"
   ```

The admin is served on port 8080 (`ports: "8080:8080"` in the compose file).

## Image variants

Two variants are published to `ghcr.io/voidrot/chorecast`. Both run as uid 65532.

| Variant | Tags | Contents |
|---|---|---|
| `runtime` (default) | `X.Y.Z`, `X.Y`, `latest` | Distroless static image with the server binary only. |
| `media` | `X.Y.Z-media`, `X.Y-media`, `latest-media` | Alpine with ffmpeg installed. |

The `media` variant is for video transcoding. The server doesn't call ffmpeg yet. Video evidence isn't available until a later release, and a chore that requires a video is refused with `video_evidence_unsupported`. Use the default `runtime` image unless you are preparing for that release.

To switch variants, change the `image:` line in the compose file and run `docker compose up -d`. Both variants keep the same `/data` volume.

## Data volume

The compose file stores everything in the named volume `chorecast-data`, mounted at `/data`:

- `chorecast.db`, the SQLite database (WAL mode).
- `media/`, the uploaded photos.
- `backups/`, the scheduled backups. See [Backups and restore](backups.md).

The container runs as uid 65532 in both variants, and the server writes to `/data` as that user. Run `chorecast backup` and `chorecast restore` with `docker compose run`, which runs them as the same user. A command run as another user can leave files the server can't write.

## Environment reference

The server reads these variables at startup. An invalid value (not a number, a non-positive count, or a duration outside `0`–`24h`) logs a warning and uses the default.

| Variable | Default | Purpose |
|---|---|---|
| `CHORECAST_ADDR` | `:8080` | Listen address. The image sets it to `:8080`. |
| `CHORECAST_DATA_DIR` | `./data` | Directory for the database, media and backups. The image sets it to `/data`. |
| `CHORECAST_PUBLIC_URL` | empty | Address browsers and the app use to reach the server, with no trailing slash. Printed links use it. When empty, links use `http://localhost:<port>`. |
| `CHORECAST_MEDIA_MAX_PHOTO_MB` | `15` | Largest accepted photo upload, in MiB. |
| `CHORECAST_BACKUP_AFTER_DAY_END` | `1h` | Delay after the household day end before the daily backup runs. Any Go duration from `0` up to `24h`. |
| `CHORECAST_BACKUP_KEEP` | `7` | Number of scheduled backups kept in `backups/`. Older ones are deleted. |
| `CHORECAST_OIDC_ISSUER` | empty | OIDC issuer URL. SSO is on only when this and `CHORECAST_OIDC_CLIENT_ID` are both set. See [Single sign-on](sso.md). |
| `CHORECAST_OIDC_CLIENT_ID` | empty | OIDC client ID. |
| `CHORECAST_OIDC_CLIENT_SECRET` | empty | OIDC client secret. Keep it out of shared files. |
| `CHORECAST_DEV` | `false` | Development only. Relaxes cookie security and allows the Vite dev server's origin. Don't set it on a server others use. |

Two more variables come from the container or Go runtime, not from Chorecast:

- `TZ` sets the container's timezone. Household timezones are set in the setup wizard.
- `GOMEMLIMIT` caps the Go heap. Set it when you set a container memory limit. See [Raspberry Pi notes](#raspberry-pi-notes).

Set a variable in the `environment:` section of the compose file. Restart the container afterwards.

## Raspberry Pi notes

- Use 64-bit Raspberry Pi OS, or another 64-bit Linux. The images are arm64 builds. A 32-bit OS has no matching image.
- Keep the data volume on fast storage. An SSD on USB is better than an SD card. The default backups are written to the same volume.
- If you set a memory limit on the container, set `GOMEMLIMIT` a little below it, so Go frees memory before the container is killed. The compose file shows an example: `mem_limit: 512m` with `GOMEMLIMIT: 400MiB`.
- The compose file notes that processing one photo peaks at about 210 MB, and that photos are processed one at a time. UNVERIFIED: the figure comes from the comment in `deploy/compose.yaml` and I did not measure it.
