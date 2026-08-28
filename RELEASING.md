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

# Arch Linux: build from source via AUR, or install the .pkg.tar.zst from
# the GitHub release directly
pacman -U terminalika-<version>-1-<arch>.pkg.tar.zst

# or download a binary from the GitHub release page
```

## Arch Linux (AUR)

The package sources live in `packaging/aur/` (`PKGBUILD` + `.SRCINFO`). To
publish them to the AUR you need an [Arch Linux account](https://aur.archlinux.org)
with an SSH key registered, then:

```sh
cd packaging/aur
# build + verify locally
makepkg -f
makepkg --printsrcinfo > .SRCINFO   # keep in sync after PKGBUILD edits

# one-time: clone the AUR package repo (needs your SSH key)
git clone ssh://aur@aur.archlinux.org/terminalika.git
cp PKGBUILD .SRCINFO terminalika/
cd terminalika
# bump pkgver / sha256sums for each new release (see below), then:
git add PKGBUILD .SRCINFO
git commit -m "Update to vX.Y.Z"
git push origin master   # AUR uses master
```

After a new launcher release, update `pkgver` and the `sha256sums` entry:

```sh
cd packaging/aur
curl -sL -o /tmp/t.tar.gz https://github.com/terminalika/terminalika/archive/vX.Y.Z.tar.gz
sha256sum /tmp/t.tar.gz
# paste the hash into PKGBUILD, bump pkgver, then:
makepkg --printsrcinfo > .SRCINFO
```

Arch users install with any AUR helper:

```sh
paru -S terminalika   # or yay -S terminalika
```

> The `go` directive lives at `1.24.0` because `tcell v2.13.10` requires it.
> Do not raise it above what a current stable Go release provides, or CI and
> `go install` for end users will break.
