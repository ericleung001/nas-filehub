# Contributing

After every intentional change (feature, fix, or docs that ship with the product):

1. Update **CHANGELOG.md** under `[Unreleased]` or a dated release section (Keep a Changelog, English).
2. Commit with a clear English message.
3. Push to `origin` `main` on the public GitHub repository [ericleung001/nas-filehub](https://github.com/ericleung001/nas-filehub).

Never commit secrets (`.env`, `.deploy*`, `.agent-login*`), local binaries (`server`, `nas-filehub`), or `screenshots/`. Prefer documenting deploy notes without embedding credentials.
