# Push to GitHub

Repo is already on GitHub as private `nas-filehub`. From this tree:

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
gh repo create nas-filehub --private --source=. --remote=origin --push
```

Always update `CHANGELOG.md` before committing feature work — see [CONTRIBUTING.md](CONTRIBUTING.md).
