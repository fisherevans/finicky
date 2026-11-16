#!/bin/bash
# Exercise the actual WebKit UI and native bridge without showing a window.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/finicky-ui-test.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT
xcrun clang -fobjc-arc -Wno-deprecated-declarations \
	-framework Cocoa -framework WebKit \
	"$repo_root/apps/finicky/tests/window-ready.m" -o "$test_dir/window-ready"
for scenario in normal delayed missing; do
	echo "Testing UI startup: $scenario"
	"$test_dir/window-ready" "$repo_root/packages/finicky-ui/dist" "$scenario"
done
