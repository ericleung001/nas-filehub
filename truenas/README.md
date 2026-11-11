# TrueNAS Apps packaging

These files are the Filehub app package submitted to the official [TrueNAS Apps catalog](https://github.com/truenas/apps) (community train).

- Issue: https://github.com/truenas/apps/issues/6453
- Pull request: https://github.com/truenas/apps/pull/6454
- Catalog path: `ix-dev/community/filehub`
- App name: `filehub`
- Image pin: `ghcr.io/ericleung001/nas-filehub:0.4.4`
- Default WebUI host port in the catalog wizard: `30568`
- Install requires an initial admin username and password (seeds the first admin on first boot)
- Run-as user and group are configurable in the wizard (default `568`, the TrueNAS `apps` user)

This copy is for reference. Filehub is listed on the [TrueNAS Apps Market](https://apps.truenas.com/catalog/filehub_community/) after [PR #6454](https://github.com/truenas/apps/pull/6454) was merged. Future packaging updates should stay in sync with the official `ix-dev/community/filehub` directory.
