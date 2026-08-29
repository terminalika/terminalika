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
5. `terminalika.dev` is served by Cloudflare Pages (its own git integration
   builds `terminalika/website` on every push to `main` - no GitHub Actions
   involved on that side). To have a release here also refresh the site,
   create a deploy hook in the Cloudflare Pages project (Settings → Builds
   and deployments → Deploy hooks, branch `main`) and add it as the
   `CF_PAGES_DEPLOY_HOOK` secret in `terminalika/terminalika` (Settings →
   Secrets and variables → Actions). Optional: skipped silently if unset.

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
- pushes a Scoop manifest to `terminalika/scoop-bucket`,
- if `CF_PAGES_DEPLOY_HOOK` is set (see one-time setup above), triggers a
  Cloudflare Pages rebuild of `terminalika/website`, which picks up this
  release as "latest" at build time (`website/src/lib/version.ts`) —
  nothing to edit there by hand.

Users then install with:

```sh
# Homebrew (macOS/Linux)
brew tap terminalika/tap && brew install --cask terminalika

# Scoop (Windows)
scoop bucket add terminalika https://github.com/terminalika/scoop-bucket
scoop install terminalika

# Debian / Ubuntu — direct .deb download (no repository needed)
wget https://github.com/terminalika/terminalika/releases/latest/download/terminalika_amd64.deb
sudo apt install ./terminalika_amd64.deb

# Fedora / RHEL — direct .rpm download
wget https://github.com/terminalika/terminalika/releases/latest/download/terminalika_amd64.rpm
sudo dnf install ./terminalika_amd64.rpm

# Arch — pacman installs the release's .pkg.tar.zst directly
wget https://github.com/terminalika/terminalika/releases/latest/download/terminalika_amd64.pkg.tar.zst
sudo pacman -U terminalika_amd64.pkg.tar.zst

# or download a binary from the GitHub release page
```

> The AUR is not used: it is closed to new packages, and the `.deb`/`.rpm`/
> `.pkg.tar.zst` downloads above cover every Linux distro directly.

> `archives.name_template` and `nfpms.file_name_template` in
> `.goreleaser.yaml` deliberately omit the version, so every asset name above
> is stable across releases and `releases/latest/download/<name>` always
> resolves — nothing here or in the website needs editing after a release.
> Every file name also uses the plain `amd64`/`arm64` pair for every
> format, not each distro's native `x86_64`/`aarch64` spelling — cosmetic
> only, package managers read the real architecture from the package
> metadata.

> The `go` directive lives at `1.24.0` because `tcell v2.13.10` requires it.
> Do not raise it above what a current stable Go release provides, or CI and
> `go install` for end users will break.
