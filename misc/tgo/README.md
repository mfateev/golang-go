# Precompiled tgo distributions

Each release contains the complete forked Go toolchain for macOS and Linux on
arm64 and amd64. Users need no existing Go installation and do not build the
compiler during installation. Application builds still compile Go packages;
cgo applications require the platform's C compiler and development libraries.

The archives contain `go/bin/tgo`, `go/bin/gofmt`, compiler/linker tools,
standard library and isolate sources, headers, licenses, and version metadata.
The `go` directory is the archive root used by Go's existing `cmd/distpack`.
The launcher selects this toolchain's GOROOT and sets `GOTOOLCHAIN=local`.

## Install on Linux or macOS

After publishing a release, download its installer and run it:

```sh
curl -fsSL https://github.com/mfateev/golang-go/releases/latest/download/install.sh -o install-tgo.sh
sh install-tgo.sh
export PATH="$HOME/.local/bin:$PATH"
tgo version
```

The installer selects the host platform, downloads the archive and `SHA256SUMS`
over HTTPS, verifies SHA-256, and installs under
`$HOME/.local/lib/tgo/<tag>/go`. It creates `$HOME/.local/bin/tgo`. The checksums
detect corruption; they rely on the GitHub release as their trust source.

To select a version, including a prerelease, or change the prefix:

```sh
sh install-tgo.sh --version tgo-v0.1.0 --prefix "$HOME/tools"
```

For offline installation, download the archive for your platform and
`SHA256SUMS` into a directory, then pass
`--from-dir /path/to/downloads --version tgo-v0.1.0`.

Upgrading to a new tag updates the launcher and retains previous installations.
Reinstalling an existing tag requires removing that tag's directory first.
To uninstall, remove the installed `bin/tgo` launcher and `lib/tgo` directory
under your chosen prefix. The bundled `gofmt` is available under that tag's
`go/bin` directory.

## Homebrew

The release workflow generates `tgo.rb` with platform-specific download URLs
and actual archive SHA-256 values. Installation copies the precompiled
toolchain into `libexec` and exposes a `tgo` launcher. It requires no Go build.
See the [Homebrew formula cookbook](https://docs.brew.sh/Formula-Cookbook).

To make the standard command available, create the `mfateev/homebrew-tgo`
repository and commit the generated file as `Formula/tgo.rb`. Update that file
from each published release. Then users can run:

```sh
brew install mfateev/tgo/tgo
```

Until that tap exists, users can install through a local tap:

```sh
brew tap-new local/tgo
curl -fsSL https://github.com/mfateev/golang-go/releases/latest/download/tgo.rb \
  -o "$(brew --repo local/tgo)/Formula/tgo.rb"
brew install local/tgo/tgo
```

The generated formula supports Homebrew on Linux too. Releases are currently
unsigned; macOS signing and notarization are separate release work.

## Build and publish

The `tgo release` workflow builds natively on four GitHub runners. Each job
builds a tracked source snapshot with `src/make.bash -distpack`, installs its
archive into a fresh path containing spaces, and compiles/runs a program that
imports `isolate`. This checks installation and relocation; it does not replace
the runtime regression suites or the cross-architecture replay gates.

Tag names use `tgo-v<semver>`, for example `tgo-v0.1.0` or
`tgo-v0.1.0-rc.1`. Push a release tag on the desired committed revision or
dispatch the workflow with an existing tag. After all builds succeed, the
workflow creates a **draft** GitHub release containing:

- Four `tgo_<version>_<os>_<arch>.tar.gz` archives.
- `install.sh`, `tgo.rb`, `SHA256SUMS`, and `release.json`.

Review and publish the draft in GitHub, then copy its `tgo.rb` to the Homebrew
tap. Drafts and prereleases are not selected by `/releases/latest`; prerelease
installers must be downloaded from the tag-specific release URL. Rerunning the
workflow can replace draft assets, but refuses to modify a published release.

Build locally with Python 3.11+, Git, a C compiler, and Go 1.26.7 as bootstrap:

```sh
export GOROOT_BOOTSTRAP=/path/to/bootstrap/go
python3 misc/tgo/release.py build tgo-v0.1.0 --output /tmp/tgo-dist
```

The build includes only committed files. It stages the sources in a temporary
directory and leaves the development toolchain untouched. The binary Go version
records the upstream development version, tgo release version, and commit.
Archive timestamps come from that commit; `GO_BUILDER_NAME` enables Go's release
path trimming. Bit-for-bit reproducibility across separate builds has not been
established. Run the build once per target, collect the four archives, then run:

```sh
python3 misc/tgo/release.py prepare tgo-v0.1.0 --output /tmp/tgo-dist
```

Preparation rejects archives with mismatched tags, targets, commits, or Go
versions. It creates the checksums and Homebrew formula from those archives.
