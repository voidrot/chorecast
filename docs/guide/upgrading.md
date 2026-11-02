# Upgrading

Read this before you change the image tag. The steps below take a backup first, so you can roll back.

## Image tags

Every server release publishes these tags to `ghcr.io/voidrot/chorecast`:

| Tag | Moves when | Use it for |
|---|---|---|
| `X.Y.Z`, for example `0.2.0` | Never | Pinning to an exact release. |
| `X.Y`, for example `0.2` | Each patch release in that minor line | Taking fixes without minor changes. |
| `latest` | Each release | Following the newest release. |

Each tag has a `-media` variant (`X.Y.Z-media`, `X.Y-media`, `latest-media`) with ffmpeg installed. See [Image variants](install.md#image-variants).

Before 1.0, a `feat` change bumps the minor version, and a `fix` bumps the patch. Breaking changes also bump the minor version. So a `0.x` move can change behavior. Pin `X.Y.Z` in production and read the changelog before you change it.

## Before you upgrade

1. **Read the changelog.** The server changes are in `server/CHANGELOG.md`. The app changes are in `android/CHANGELOG.md`. Each release also gets a GitHub release, created by release-please.
2. **Take a backup.** It can be taken while the server runs:

   ```sh
   docker compose -f deploy/compose.yaml run --rm chorecast backup
   ```

   Keep a copy outside `backups/`, because the scheduled pruning deletes old files there. See [Backups and restore](backups.md).

## Upgrade the server

1. Change the `image:` line in `deploy/compose.yaml` to the new tag.
2. Pull the image and restart:

   ```sh
   docker compose -f deploy/compose.yaml pull
   docker compose -f deploy/compose.yaml up -d
   ```

3. Check the log for `listening`, and for errors from the migrations:

   ```sh
   docker compose -f deploy/compose.yaml logs --tail 50 chorecast
   ```

Database migrations run automatically at startup, before the server listens. You don't run anything by hand. Migrations are embedded in the binary.

## Roll back

If the new version doesn't work, stop the server, set the image tag back to the old version, and restore the backup you took before the upgrade:

```sh
docker compose -f deploy/compose.yaml stop chorecast
docker compose -f deploy/compose.yaml run --rm chorecast restore --force /data/backups/<backup-file>
docker compose -f deploy/compose.yaml up -d
```

Restore an archive with the old tag, not the new one. A restore refuses an archive whose schema is newer than the binary doing the restore.

## Upgrade the Android app

Install the new APK for the same flavor you already use. Each Android release is attached to its `android-vX.Y.Z` GitHub release. Obtainium does the same thing if you use it. See [Install the Android app](first-run.md#install-the-android-app).

## Before 1.0: schema changes

Before 1.0 the database schema is still being built. The project keeps one migration file, `server/internal/db/migrations/00001_init.sql`, and edits it in place rather than adding new migrations. That's by design, because there is no data to migrate yet (`docs/handoff.md`).

For you, this means an edited `00001` doesn't reach a database that already has it applied. goose records that migration as done, so a database created by an older version keeps its old schema. A release that changes the schema can therefore leave an existing database incompatible with the new version. Check the release's changelog before you upgrade. If the release needs a fresh database, start it on a new data directory, which means a fresh install. Keep the old volume until you're sure you don't need it.

The policy changes at 1.0. From then on, schema changes are forward-only migrations (`docs/roadmap.md`), and upgrades migrate the existing data.
