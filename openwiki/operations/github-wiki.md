# GitHub Wiki Publishing

GitHub Wikis are separate Git repositories. For Arivu, keep `openwiki/` as the
source of truth in the main repository, then publish selected user-facing pages
to `glnarayanan/arivu.wiki.git`.

## Page Map

- `Home.md`: summarize `README.md` plus `openwiki/user-guide.md`.
- `Getting-Started.md`: use `openwiki/user-guide.md` and
  `openwiki/operations/deployment.md`.
- `Using-Arivu.md`: use `openwiki/user-guide.md` and
  `openwiki/workflows/second-brain-loop.md`.
- `Import-Export-and-Migration.md`: use `openwiki/domain/migration-guide.md`.
- `Security.md`: use `openwiki/workflows/security-model.md` and
  `openwiki/workflows/auth-security.md`.
- `Developer-Architecture.md`: use `openwiki/quickstart.md`,
  `openwiki/architecture/runtime.md`, and `openwiki/architecture/frontend.md`.

## Automatic Sync

The repository includes `.github/workflows/wiki-sync.yml`. After changes land
on `main`, the workflow runs `scripts/sync-github-wiki.sh`, clones the existing
GitHub Wiki remote, copies the generated Markdown pages, and pushes only when
the generated pages changed.

The sync is intentionally curated. It publishes the public pages in the map
above instead of dumping every `openwiki/` page into the GitHub Wiki.

## Manual Publish

Clone the wiki repository next to the app repository:

```bash
git clone git@github.com:glnarayanan/arivu.wiki.git ../arivu.wiki
```

Copy or adapt the mapped pages into `../arivu.wiki/*.md`, then push:

```bash
cd ../arivu.wiki
git add .
git commit -m "docs: publish Arivu user wiki"
git push
```

If the wiki repo does not exist yet, enable Wikis in the GitHub repository
settings, create the first page in the browser, then clone it.

## Maintenance Rule

Do not edit the GitHub Wiki as the canonical copy. Update `README.md` or
`openwiki/` first, then let the workflow republish the relevant wiki page. This
avoids drift between repository documentation and the public wiki.
