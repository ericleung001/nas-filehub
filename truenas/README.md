# TrueNAS Apps packaging

These files are the Filehub app package submitted to the official [TrueNAS Apps catalog](https://github.com/truenas/apps) (community train).

- Issue: https://github.com/truenas/apps/issues/6453
- Pull request (initial listing): https://github.com/truenas/apps/pull/6454
- Catalog path: `ix-dev/community/filehub`
- App name: `filehub`
- Default WebUI host port in the catalog wizard: `30568`
- Install requires an initial admin username and password (seeds the first admin on first boot)
- Run-as user and group are configurable in the wizard (default `568`, the TrueNAS `apps` user)

This copy is a reference snapshot of the catalog package (currently pinned to image tag `0.4.7` in `app.yaml` / `ix_values.yaml`) and may lag the live [TrueNAS Apps Market](https://apps.truenas.com/catalog/filehub_community/) listing. Once Filehub is listed, already-published versions follow new GitHub releases automatically. Do not open catalog pull requests just to bump the app version.

The Market listed Filehub after [PR #6454](https://github.com/truenas/apps/pull/6454) was merged. For the current app and catalog versions, see the Market page rather than this snapshot.
