#!/bin/sh
# Build the packages that get published.
#
#   sh build.sh          a local build, version reads "unreleased"
#   sh build.sh v1       stamped with a version
#
# The release workflow calls this same script rather than repeating what it
# does. Two copies of a build recipe drift apart, and by the time anyone
# notices, some release has been going out wrong for months.

set -e
cd "$(dirname "$0")"
rm -rf dist
mkdir -p dist

# -s -w drops the debug information; the binary is a lot smaller for it.
FLAGS="-s -w"

VERSION="${1:-$GITHUB_REF_NAME}"
if [ -n "$VERSION" ]; then
    FLAGS="$FLAGS -X main.version=$VERSION"
    echo "Version: $VERSION"
else
    echo "Version: unreleased (no tag given)"
fi
echo ""

echo "Checking before building..."
if [ -n "$(gofmt -l .)" ]; then
    echo "  these files are not formatted:"
    gofmt -l .
    exit 1
fi
go vet ./...
go test ./... >/dev/null
echo "  all good"
echo ""

# pack <folder> <os> <arch> <binary name>
pack() {
    FOLDER="dist/$1"
    mkdir -p "$FOLDER"
    # -trimpath keeps the path this was built from out of the binary. In a
    # tool about not leaking things, shipping the author's directory inside
    # the executable would be a poor way to start.
    GOOS="$2" GOARCH="$3" go build -trimpath -ldflags "$FLAGS" -o "$FOLDER/$4" .
    cp README.md LICENSE "$FOLDER/"
    echo "  $1"
}

echo "Building..."
pack tally-windows   windows amd64 tally.exe
pack tally-linux     linux   amd64 tally
pack tally-linux-arm linux   arm64 tally
pack tally-mac-apple darwin  arm64 tally
pack tally-mac-intel darwin  amd64 tally

echo ""
echo "Compressing..."
cd dist
for P in tally-windows tally-linux tally-linux-arm \
         tally-mac-apple tally-mac-intel; do
    if [ "$P" = "tally-windows" ]; then
        # zip first: it is what Linux and the workflow have. powershell is
        # the fallback for building on Windows, where zip is not standard.
        # Never tar: the tar on Linux cannot write a zip and would leave a
        # broken file without saying a word.
        if command -v zip >/dev/null 2>&1; then
            zip -qr "$P.zip" "$P"
        elif command -v powershell >/dev/null 2>&1; then
            powershell -NoProfile -Command \
                "Compress-Archive -Path '$P' -DestinationPath '$P.zip' -Force" >/dev/null
        else
            echo "  found neither zip nor powershell to compress $P"
            exit 1
        fi
    else
        tar -czf "$P.tar.gz" "$P"
    fi
done
cd ..

echo ""
for F in dist/*.zip dist/*.tar.gz; do
    [ -f "$F" ] && printf "  %-30s %s\n" "$(basename "$F")" "$(du -h "$F" | cut -f1)"
done
echo ""
echo "Done."
