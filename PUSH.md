# Push to GitHub

Repo is already on GitHub as public [`ericleung001/nas-filehub`](https://github.com/ericleung001/nas-filehub). From this tree:

```bash
cd /workspace/nas-filehub
git add -A   # after checking `git status` — do not add secrets/binaries
git status
git commit -m "your message"
git push -u origin main
```

If `gh` is not logged in yet:

```bash
gh auth login
gh repo create nas-filehub --public --source=. --remote=origin --push
```

Always update `CHANGELOG.md` before committing feature work — see [CONTRIBUTING.md](CONTRIBUTING.md).
