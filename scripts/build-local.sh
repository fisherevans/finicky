#!/bin/bash
# Build and ad-hoc sign locally. Never replace or launch an installed app.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"
if [ "$(uname -s)" != Darwin ]; then
    echo 'This build requires macOS.' >&2
    exit 1
fi
for build_tool in node npm go xcrun codesign ditto; do
    if ! command -v "$build_tool" >/dev/null 2>&1; then
        echo "Missing dependency: $build_tool. See LOCAL-INSTALL.md." >&2
        exit 1
    fi
done
xcrun --find clang >/dev/null
node -e 'const [major, minor] = process.versions.node.split(".").map(Number); if (!((major === 22 && minor >= 12) || major === 24 || major >= 26)) { console.error("Node.js 22.12+, 24.x, or 26+ is required."); process.exit(1); }'

echo 'Installing dependencies from the committed lockfiles...'
(cd packages/config-api && npm ci && npm run build && npm run generate-types)
(cd packages/finicky-ui && npm ci && npm run build)
mkdir -p apps/finicky/src/assets/templates
cp packages/config-api/dist/finickyConfigAPI.js apps/finicky/src/assets/finickyConfigAPI.js
cp -R packages/finicky-ui/dist/. apps/finicky/src/assets/templates/

export CGO_ENABLED=1
export GOOS=darwin
export GOARCH=$(go env GOHOSTARCH)
export CGO_CFLAGS="${CGO_CFLAGS:-} -mmacosx-version-min=12.0"
export CGO_LDFLAGS="${CGO_LDFLAGS:-} -mmacosx-version-min=12.0"
echo 'Running tests; no browser is opened...'
npm --prefix packages/config-api test -- --run
go test -C apps/finicky/src -mod=readonly ./...

mkdir -p build/local
build_stage=$(mktemp -d "$repo_root/build/local/.build.XXXXXX")
trap 'rm -rf "$build_stage"' EXIT
app="$build_stage/Finicky.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp -R apps/finicky/assets/. "$app/Contents/"
# Bundle resources belong in Resources, not the directory reserved for nested code.
if [ -f "$app/Contents/template-icon.pdf" ]; then
    mv "$app/Contents/template-icon.pdf" "$app/Contents/Resources/"
fi
cp packages/config-api/dist/finicky.d.ts "$app/Contents/Resources/finicky.d.ts"
revision=source-archive
if [ -e .git ]; then
    revision=$(git rev-parse --short=12 HEAD)
    if ! git diff --quiet HEAD --; then revision="$revision-dirty"; fi
elif [ -f SOURCE_REVISION ]; then
    revision=$(head -n 1 SOURCE_REVISION)
    case "$revision" in
        *[!0-9a-f]*|'') echo 'Invalid SOURCE_REVISION.' >&2; exit 1 ;;
    esac
fi
build_date=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
# Empty apiHost keeps automatic upstream update checks disabled for this fork.
go build -C apps/finicky/src -mod=readonly \
    -ldflags "-X finicky/version.commitHash=$revision -X finicky/version.buildDate=$build_date -X finicky/version.apiHost=" \
    -o "$app/Contents/MacOS/Finicky"
codesign --force --sign - --options runtime --timestamp=none "$app"
codesign --verify --deep --strict "$app"

output="$repo_root/build/local/Finicky.app"
if [ -e "$output" ] || [ -L "$output" ]; then
    # Only replace the generated output in this checkout.
    rm -rf "$output"
fi
mv "$app" "$output"
echo "Built and verified: $output"
echo 'The installed app and routing configuration were not changed.'
echo 'Follow LOCAL-INSTALL.md to back up and replace your installed app.'
