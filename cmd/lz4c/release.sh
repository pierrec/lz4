#!/bin/sh
# Release lz4c against a new lz4 tag: bump cmd/lz4c/go.mod to it, check
# that lz4c builds and round-trips a file, commit the bump and tag the next
# cmd/lz4c patch version. .github/workflows/lz4c-release.yml runs it when a
# v4 tag is pushed. It does nothing for pre-releases or for a version lz4c
# already requires, so reruns are safe.
#
# lz4c tags stay below v2: its module path has no /v4 suffix.
#
# Usage: cmd/lz4c/release.sh [-n] v4.X.Y
#   -n  dry run: stop before committing, tagging and pushing.
set -eu

dry=
if [ "${1:-}" = -n ]; then
	dry=1
	shift
fi
lib=${1:?usage: release.sh [-n] v4.X.Y}

case $lib in
v4.*-* | v4.*+*)
	echo "skipping pre-release $lib"
	exit 0
	;;
v4.*.*) ;;
*)
	echo "not a v4 release tag: $lib" >&2
	exit 1
	;;
esac

cd "$(dirname "$0")"

cur=$(go list -m -f '{{.Version}}' github.com/pierrec/lz4/v4)
newest=$(printf '%s\n%s\n' "$cur" "$lib" | sort -V | tail -n 1)
if [ "$lib" = "$cur" ] || [ "$newest" != "$lib" ]; then
	echo "lz4c already requires lz4 $cur; nothing to do for $lib"
	exit 0
fi

# The proxy and checksum database fetch a new tag on first request, which
# can briefly fail right after it is pushed.
i=0
until go get "github.com/pierrec/lz4/v4@$lib"; do
	i=$((i + 1))
	if [ "$i" -ge 5 ]; then
		echo "could not fetch lz4 $lib" >&2
		exit 1
	fi
	sleep 30
done
go mod tidy
go vet ./...

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
go build -o "$tmp/lz4c" .
cp ../../README.md "$tmp/data"
"$tmp/lz4c" compress "$tmp/data" > /dev/null
rm "$tmp/data"
"$tmp/lz4c" uncompress "$tmp/data.lz4" > /dev/null
cmp "$tmp/data" ../../README.md

last=$(git tag -l 'cmd/lz4c/v1.*' | sed 's|^cmd/lz4c/v||' | grep -v '[-+]' | sort -V | tail -n 1)
if [ -z "$last" ]; then
	next=v1.0.0
else
	next=v${last%.*}.$((${last##*.} + 1))
fi
tag=cmd/lz4c/$next

echo "lz4c $next: lz4 $cur -> $lib"
git diff --stat .
if [ -n "$dry" ]; then
	echo "dry run: not committing $tag"
	exit 0
fi

git commit -q -m "cmd/lz4c: build against lz4 $lib" go.mod go.sum
git tag -a "$tag" -m "lz4c $next, built against lz4 $lib

Install it with:

    go install github.com/pierrec/lz4/cmd/lz4c@latest"
git push --atomic origin HEAD:refs/heads/v4 "refs/tags/$tag"
