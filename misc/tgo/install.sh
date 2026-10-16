#!/bin/sh
# Install a precompiled tgo toolchain. No Go compiler or root access required.
set -eu

fail() { echo "tgo: $*" >&2; exit 1; }
usage() {
    echo "Usage: sh install.sh [--version tgo-v0.1.0] [--prefix PATH] [--from-dir PATH]"
    echo "Default prefix: \$HOME/.local. --from-dir uses downloaded release assets."
}

version=latest
prefix=${HOME:?HOME must be set}/.local
from_dir=
repository=${TGO_REPOSITORY:-mfateev/golang-go}
while [ "$#" -gt 0 ]; do
    case "$1" in
        --version|--prefix|--from-dir)
            [ "$#" -ge 2 ] || fail "missing value for $1"
            case "$1" in
                --version) version=$2 ;;
                --prefix) prefix=$2 ;;
                --from-dir) from_dir=$2 ;;
            esac
            shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) fail "unknown argument: $1" ;;
    esac
done
case "$(uname -s)" in
    Darwin) system=darwin ;;
    Linux) system=linux ;;
    *) fail "supported systems: macOS and Linux" ;;
esac
case "$(uname -m)" in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64) arch=amd64 ;;
    *) fail "supported architectures: arm64 and amd64" ;;
esac
if [ -n "$from_dir" ]; then
    [ "$version" != latest ] || fail "--from-dir requires --version"
else
    command -v curl >/dev/null || fail "curl is required"
    if [ "$version" = latest ]; then
        latest=$(curl --proto '=https' --tlsv1.2 -fsSL -o /dev/null -w '%{url_effective}' "https://github.com/$repository/releases/latest")
        version=${latest##*/}
    fi
fi
echo "$version" | LC_ALL=C grep -Eq '^tgo-v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$' || fail "invalid release tag: $version"
number=${version#tgo-v}
archive=tgo_${number}_${system}_${arch}.tar.gz
work=$(mktemp -d)
staging=
cleanup() {
    rm -rf "$work"
    [ -z "$staging" ] || rm -rf "$staging"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
if [ -n "$from_dir" ]; then
    cp "$from_dir/$archive" "$work/$archive"
    cp "$from_dir/SHA256SUMS" "$work/SHA256SUMS"
else
    base=https://github.com/$repository/releases/download/$version
    curl --proto '=https' --tlsv1.2 -fSL "$base/$archive" -o "$work/$archive"
    curl --proto '=https' --tlsv1.2 -fsSL "$base/SHA256SUMS" -o "$work/SHA256SUMS"
fi
expected=$(awk -v name="$archive" '$2 == name {print $1}' "$work/SHA256SUMS")
[ "${#expected}" -eq 64 ] || fail "missing or invalid checksum for $archive"
case "$expected" in *[!0-9a-f]*) fail "invalid SHA-256 checksum" ;; esac
if command -v sha256sum >/dev/null; then
    actual=$(sha256sum "$work/$archive" | cut -d ' ' -f 1)
elif command -v shasum >/dev/null; then
    actual=$(shasum -a 256 "$work/$archive" | cut -d ' ' -f 1)
else
    fail "sha256sum or shasum is required"
fi
[ "$expected" = "$actual" ] || fail "checksum mismatch for $archive"
mkdir -p "$prefix/lib/tgo" "$prefix/bin"
prefix=$(CDPATH= cd "$prefix" && pwd)
destination=$prefix/lib/tgo/$version
[ ! -e "$destination" ] || fail "$destination already exists; remove it to reinstall"
staging=$(mktemp -d "$prefix/lib/tgo/.install-XXXXXX")
tar -xzf "$work/$archive" -C "$staging"
[ -x "$staging/go/bin/tgo" ] || fail "archive is missing bin/tgo"
[ -d "$staging/go/src/isolate" ] || fail "archive is missing the isolate runtime"
mv "$staging" "$destination"
staging=
# Use the wrapper's location so the prefix can contain spaces or be relocated.
cat > "$work/tgo" <<EOF
#!/bin/sh
set -eu
root=\$(CDPATH= cd -- "\$(dirname -- "\$0")/../lib/tgo/$version/go" && pwd)
export GOROOT=\$root
export GOTOOLCHAIN=local
exec "\$root/bin/tgo" "\$@"
EOF
chmod 755 "$work/tgo"
# Stage the command on the destination filesystem before replacing it.
command_stage=$(mktemp "$prefix/bin/.tgo-XXXXXX")
cp "$work/tgo" "$command_stage"
chmod 755 "$command_stage"
mv -f "$command_stage" "$prefix/bin/tgo"
echo "Installed $version in $destination"
echo "Add $prefix/bin to PATH, then run: tgo version"
