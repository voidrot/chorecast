# Backups and restore

Chorecast backs up its database and media on a schedule, and the `chorecast` binary can write and restore a backup by hand. There is no backup download in the web admin, by design.

## Scheduled backups

The server backs up once a day, at the household day end plus `CHORECAST_BACKUP_AFTER_DAY_END` (default `1h`). Each backup is written to `backups/` in the data directory, as `chorecast-<UTC time>.tar.gz`. Only the newest `CHORECAST_BACKUP_KEEP` files are kept (default 7).

- A backup that is more than 24 hours late is skipped. The next one covers the same data.
- A failed backup is logged and retried, up to three attempts.
- Pruning counts every `chorecast-<time>.tar.gz` in `backups/`, including backups you made by hand. A manual backup left there can be deleted by pruning. Keep manual backups outside `backups/`, or use `--out` to write them elsewhere.

In Docker, the backups are in the `chorecast-data` volume at `/data/backups`.

## Take a backup by hand

```sh
docker compose -f deploy/compose.yaml run --rm chorecast backup
```

The command writes into `/data/backups` by default and prints the file name, size, schema version and table count. It is safe while the server runs. If no server is running, it holds the data directory's lock until it finishes, so a server can't start mid-backup.

To write the archive somewhere else in the data volume, pass `--out`:

```sh
docker compose -f deploy/compose.yaml run --rm chorecast backup --out /data/backups/before-upgrade.tar.gz
```

The file is written with owner-only permissions. The command refuses to overwrite an existing file.

To keep a copy off the host, copy it out of the volume. UNVERIFIED: the copy command below. I did not run it, and the volume name depends on the compose project name.

```sh
docker volume ls
docker run --rm -v <volume-name>:/data -v "$PWD":/out alpine cp /data/backups/<file> /out/
```

## What an archive contains

An archive is a `tar.gz` with three parts:

- `manifest.json`: the app version, the schema version, the row count for each table, and the media file count and size.
- `chorecast.db`: a consistent snapshot of the database.
- `media/`: the uploaded photos, in the same layout as the data directory.

**Archives contain secrets.** They hold the media signing key, parents' TOTP secrets, and the hashes of passwords and tokens. Anyone with an archive can forge photo links and generate two-factor codes, so store backups as privately as the data directory itself. If a backup leaks, run `chorecast admin rotate-media-key` and have each parent re-enroll two-factor sign-in.

## Restore

**Restore only archives you created.** A restore replaces every account, secret and photo on the server.

1. Stop the server. Restore refuses to run while the server holds the data directory.

   ```sh
   docker compose -f deploy/compose.yaml stop chorecast
   ```

2. Run the restore with the archive path inside the container:

   ```sh
   docker compose -f deploy/compose.yaml run --rm chorecast restore /data/backups/chorecast-<time>.tar.gz
   ```

   If the data directory already has a database or photos, restore refuses unless you add `--force`. `--force` replaces that data. It never touches the files in `backups/`.

3. Start the server again.

   ```sh
   docker compose -f deploy/compose.yaml up -d
   ```

`docker compose run` runs the command as the image's user, uid 65532, which is the user the server writes as. If you run the binary outside Docker, run it as the same user the server runs as. Files created by another user can't be written by the server.

Restore checks the archive before it changes anything:

- It accepts only the three parts above. It refuses symlinks, hard links, absolute paths and `..` in names.
- Every row count and media total must match `manifest.json`.
- An archive from a newer version of Chorecast is refused. Restore it with a newer binary. See [Upgrading](upgrading.md).
- An older archive is migrated to the current schema when it is restored.

Restore moves the existing data aside and puts the restored data in place. If something fails partway, it moves the old data back.

## Restore marker and recovery

While the data is being swapped, restore writes a marker file, `chorecast.restoring`, in the data directory. The marker records where the previous data was moved.

If restore is interrupted, the marker stays. The server, `backup`, `admin` and `restore` all refuse to start while it exists, and the error names the previous data's location and the marker's path. To recover:

1. Look at the previous data's location in the error message.
2. Decide which data to keep. To go back to the old data, move it back into the data directory. To keep the restored data, leave it where it is.
3. Delete the `chorecast.restoring` file.
4. Start the server.

If the error says that undoing the restore failed too, the previous data is in the staging directory named in the message, and the marker still blocks the server. Move the data back by hand before you delete the marker.

## Run a backup or restore without Docker

The binary reads the data directory from `CHORECAST_DATA_DIR`. Run it as the server's user, with the server stopped for `restore`:

```sh
chorecast backup --out /path/to/chorecast-manual.tar.gz
chorecast restore /path/to/chorecast-manual.tar.gz
```
