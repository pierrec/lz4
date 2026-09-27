#!/bin/sh
# Fetch the Silesia corpus as testdata/silesia.tar (211947520 bytes), the file
# the Silesia tests and benchmarks read, from klauspost.com: the same bytes
# klauspost/compress tests. The download and the tar are both checked against
# pinned SHA-256s, so every machine tests the same bytes.
#
# With -cli, also make testdata/silesia.tar.B4D.lz4 and .B7D.lz4 with the lz4
# CLI (lz4 -BD -B4 and -BD -B7: linked 64 KiB and 4 MiB blocks), for decoding
# frames this package did not encode.
#
# Needs curl and the zstd CLI, and with -cli the lz4 CLI.
#
# Usage: testdata/fetch_silesia.sh [-cli]
set -eu

url=https://klauspost.com/files/compress/silesia.tar.zst
zst_sha256=c7c7f7c3c93f629aecc8f438c571fa97f5ba6f6074b0db6371e4dd1fc079e538
tar_sha256=fc60dca8d229df75c4e8bc8ad4c60347ea7770823b97de31cea84352cf462b32

cd "$(dirname "$0")"

sha256() {
	if command -v sha256sum > /dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

if [ -f silesia.tar ] && [ "$(sha256 silesia.tar)" = "$tar_sha256" ]; then
	echo "silesia.tar is up to date"
else
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	curl -fsSL --retry 3 -o "$tmp/silesia.tar.zst" "$url"
	[ "$(sha256 "$tmp/silesia.tar.zst")" = "$zst_sha256" ] || { echo "silesia.tar.zst: SHA-256 mismatch" >&2; exit 1; }
	zstd -q -d "$tmp/silesia.tar.zst" -o "$tmp/silesia.tar"
	[ "$(sha256 "$tmp/silesia.tar")" = "$tar_sha256" ] || { echo "silesia.tar: SHA-256 mismatch" >&2; exit 1; }
	mv "$tmp/silesia.tar" .
	echo "fetched silesia.tar"
fi

if [ "${1:-}" = "-cli" ]; then
	lz4 -q -f -BD -B4 silesia.tar silesia.tar.B4D.lz4
	lz4 -q -f -BD -B7 silesia.tar silesia.tar.B7D.lz4
	echo "made silesia.tar.B4D.lz4 and silesia.tar.B7D.lz4 with $(lz4 --version 2>&1 | head -1)"
fi
