# Billy Release Checklist

Use this before creating a release tag.

## 1. Build and smoke-test the current branch

Run:

```bash
cd /Users/jonathanforrider/Programming/localai-cli/billy-app
bash scripts/release-smoke.sh
```

This covers:

- `go test ./...`
- local `go build`
- `billy --version`
- local `billy serve` startup and API checks
- optional `goreleaser release --snapshot --clean` if `goreleaser` is installed

## 2. Manual CLI checks

Launch Billy normally:

```bash
cd /Users/jonathanforrider/Programming/localai-cli/billy-app
go build -o /tmp/billy ./cmd/billy
/tmp/billy
```

Check these paths:

- welcome screen shows clear setup hints if Ollama is missing
- `/backend`
- `/backend reload`
- `/model`
- `/mode teach`
- `/hint`
- `/license`
- `/clear`
- `/session`
- `/history`

If Ollama is running, also test:

```bash
/tmp/billy "explain this repository"
```

## 3. Release artifacts

If `goreleaser` is installed:

```bash
cd /Users/jonathanforrider/Programming/localai-cli/billy-app
goreleaser release --snapshot --clean
```

Verify:

- archives land in `dist/`
- checksums are generated
- archive names match `.goreleaser.yml`

## 4. Release notes and docs

Before tagging:

- confirm README matches the shipped behavior
- confirm support/setup messaging is accurate
- confirm `billysh.online` links are correct
- prepare the devlog entry draft for the release

After the release is live:

- verify one real install from the published release
- update the devlog entry with the final version number
- publish/redeploy docs

## 5. Tag and release

After the checklist passes:

```bash
cd /Users/jonathanforrider/Programming/localai-cli/billy-app
git add .
git commit -m "feat: polish onboarding and local serve mode"
git push origin feature/custom-endpoints-alpha
git checkout main
git merge --ff-only feature/custom-endpoints-alpha
git push origin main
git tag v0.1.9
git push origin v0.1.9
```

Adjust the version tag as needed.
