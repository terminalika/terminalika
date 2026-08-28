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
4. License: both repos carry a MIT `LICENSE` file; `.goreleaser.yaml` sets
   `license: MIT` for the cask, scoop manifest and nfpm packages.

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
- attaches them (plus `deb`/`rpm`/`apk`/`archlinux` packages) to the GitHub
  release,
- pushes a Homebrew cask to `terminalika/homebrew-tap`,
- pushes a Scoop manifest to `terminalika/scoop-bucket`.

Users then install with:

```sh
# Homebrew (macOS/Linux)
brew tap terminalika/tap && brew install --cask terminalika

# Scoop (Windows)
scoop bucket add terminalika https://github.com/terminalika/scoop-bucket
scoop install terminalika

# Debian / Ubuntu — direct .deb download (no repository needed)
wget https://github.com/terminalika/terminalika/releases/download/vX.Y.Z/terminalika_X.Y.Z_amd64.deb
sudo apt install ./terminalika_X.Y.Z_amd64.deb

# Fedora / RHEL — direct .rpm download
wget https://github.com/terminalika/terminalika/releases/download/vX.Y.Z/terminalika-X.Y.Z-1.x86_64.rpm
sudo dnf install ./terminalika-X.Y.Z-1.x86_64.rpm

# Arch — pacman installs the release's .pkg.tar.zst directly
sudo pacman -U terminalika-X.Y.Z-1-x86_64.pkg.tar.zst

# or download a binary from the GitHub release page
```

> The AUR is not used: it is closed to new packages, and the `.deb`/`.rpm`/
> `.pkg.tar.zst` downloads above cover every Linux distro directly.

> The `go` directive lives at `1.24.0` because `tcell v2.13.10` requires it.
> Do not raise it above what a current stable Go release provides, or CI and
> `go install` for end users will break.
