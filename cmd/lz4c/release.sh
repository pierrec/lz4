#!/bin/sh
# Release lz4c against a new lz4 tag: bump cmd/lz4c/go.mod to it, check
# that lz4c builds and round-trips a file, commit the bump and tag the next
# cmd/lz4c version. .github/workflows/lz4c-release.yml runs it when a v4 tag
# is pushed. It does nothing for pre-releases or for a version lz4c already
# requires, so reruns are safe.
#
# The next version is a patch bump, unless cmd/lz4c/BUMP says "minor", as
# it should for a release that adds flags or raises lz4c's Go version. The
# release commit deletes BUMP, so it applies to one release.
#
# lz4c tags stay below v2: its module path has no /v4 suffix.
#
# Usage: cmd/lz4c/release.sh [-n] v4.X.Y
#        cmd/lz4c/release.sh -next
#   -n     dry run: stop before committing, tagging and pushing.
#   -next  print the next lz4c version and exit.
set -eu

dry=
next_only=
case ${1:-} in
-n)
	dry=1
	shift
	;;
-next)
	next_only=1
	shift
	;;
esac
if [ -z "$next_only" ]; then
	lib=${1:?usage: release.sh [-n] v4.X.Y | release.sh -next}
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
fi

cd "$(dirname "$0")"

bump="patch"
if [ -f BUMP ]; then
	bump=$(sed 's/#.*//' BUMP | tr -d '[:space:]')
fi
last=$(git tag -l 'cmd/lz4c/v1.*' | sed 's|^cmd/lz4c/v||' | grep -v '[-+]' | sort -V | tail -n 1 || true)
minor=${last#*.}
minor=${minor%%.*}
case $bump in
patch) next=v${last%.*}.$((${last##*.} + 1)) ;;
minor) next=v${last%%.*}.$((minor + 1)).0 ;;
*)
	echo "cmd/lz4c/BUMP says \"$bump\": want minor or patch (lz4c stays below v2)" >&2
	exit 1
	;;
esac
if [ -z "$last" ]; then
	next=v1.0.0
fi
if [ -n "$next_only" ]; then
	echo "$next"
	exit 0
fi

cur=$(go list -m -f '{{.Version}}' github.com/pierrec/lz4/v4)
# lz4c may require a pseudo-version of an unreleased commit on v4, such as
# v4.1.35-0.20261126053415-6a03abafc698, to use an API before it is tagged.
# That precedes v4.1.35 in semver, but sort -V puts it after, so compare
# without the pre-release part.
newest=$(printf '%s\n%s\n' "${cur%%-*}" "$lib" | sort -V | tail -n 1)
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

tag=cmd/lz4c/$next

echo "lz4c $next ($bump bump): lz4 $cur -> $lib"
git diff --stat .
if [ -n "$dry" ]; then
	echo "dry run: not committing $tag"
	exit 0
fi

git add go.mod go.sum
if [ -f BUMP ]; then
	git rm -q BUMP
fi
git commit -q -m "cmd/lz4c: build against lz4 $lib"
git tag -a "$tag" -m "lz4c $next, built against lz4 $lib

Install it with:

    go install github.com/pierrec/lz4/cmd/lz4c@latest"
git push --atomic origin HEAD:refs/heads/v4 "refs/tags/$tag"
