# Push to GitHub（`gh` 尚未登入時用）

在已登入 GitHub 的機器上（或先 `gh auth login`）：

```bash
cd /workspace/nas-filehub   # 或你複製後的路徑
gh auth login               # 若尚未登入
gh repo create nas-filehub --private --source=. --remote=origin --push
```

或手動：

```bash
# 先在 github.com 新建空 repo，例如 YOUR_USER/nas-filehub
git remote add origin git@github.com:YOUR_USER/nas-filehub.git
git push -u origin main
```

本機驗證：

```bash
make smoke
# 或
ADMIN_PASSWORD='your-password' make run
# 開 http://127.0.0.1:8080
```
