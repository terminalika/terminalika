# Releasing

Terminalika is two repos. The launcher depends on the **published**
`terminalika-core`, so core must be released first whenever its `go.mod`
changes.

## One-time setup

1. Make the repos public (already done).
2. Create two empty public repos under the `terminalika` org:
   - `homebrew-tap`
   - `scoop-bucket`
3. Create a Personal Access Token with `repo` scope and add it as the
   `TAP_GITHUB_TOKEN` secret in `terminalika/terminalika` (Settings →
   Secrets and variables → Actions). The default `GITHUB_TOKEN` cannot push to
   the tap/bucket repos.
4. Add a `LICENSE` file to both repos and set `license:` in
   `.goreleaser.yaml` (see the comments there).

## Releasing a new core version

```sh
cd terminalika-core
go test ./... && go vet ./...
# bump go.mod if needed, then:
git add go.mod
git commit -m "Lower go directive to 1.24.0"   # example
git tag v0.2.1
git push origin v0.2.1
```

## Releasing the launcher

After core is published:

```sh
cd terminalika
# point at the new core version and lower the go directive to match:
#   go.mod: go 1.24.0, require terminalika-core v0.2.1
go mod tidy
go test ./... && go vet ./...
git add go.mod go.sum
git commit -m "Depend on terminalika-core v0.2.1"
git tag v0.3.0
git push origin v0.3.0
```

Pushing the tag triggers `.github/workflows/release.yml`, which runs
goreleaser and:

- builds static binaries for `linux`/`darwin`/`windows` × `amd64`/`arm64`,
- attaches them (plus `deb`/`rpm`/`apk`) to the GitHub release,
- pushes a Homebrew formula to `terminalika/homebrew-tap`,
- pushes a Scoop manifest to `terminalika/scoop-bucket`.

Users then install with:

```sh
# Homebrew (macOS/Linux)
brew tap terminalika/tap && brew install --cask terminalika

# Scoop (Windows)
scoop bucket add terminalika https://github.com/terminalika/scoop-bucket
scoop install terminalika

# or download a binary from the GitHub release page
```

> The `go` directive lives at `1.24.0` because `tcell v2.13.10` requires it.
> Do not raise it above what a current stable Go release provides, or CI and
> `go install` for end users will break.
