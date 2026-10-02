#!/bin/sh
# Fetch the Silesia corpus as testdata/silesia.tar (211947520 bytes), the file
# the Silesia tests and benchmarks read, from klauspost.com: the same bytes
# klauspost/compress tests. If klauspost.com is unreachable, rebuild the same
# tar from the corpus's own silesia.zip with silesia_tar.py. Downloads and the
# tar are all checked against pinned SHA-256s, so every machine tests the same
# bytes.
#
# With -cli, also make testdata/silesia.tar.B4D.lz4 and .B7D.lz4 with the lz4
# CLI (lz4 -BD -B4 and -BD -B7: linked 64 KiB and 4 MiB blocks), for decoding
# frames this package did not encode.
#
# Needs curl and the zstd CLI (or, for the fallback, unzip and python3), and
# with -cli the lz4 CLI.
#
# Usage: testdata/fetch_silesia.sh [-cli]
set -eu

url=https://klauspost.com/files/compress/silesia.tar.zst
zst_sha256=c7c7f7c3c93f629aecc8f438c571fa97f5ba6f6074b0db6371e4dd1fc079e538
zip_url=https://sun.aei.polsl.pl/~sdeor/corpus/silesia.zip
zip_sha256=0626e25f45c0ffb5dc801f13b7c82a3b75743ba07e3a71835a41e3d9f63c77af
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
	get() { curl -fsSL --connect-timeout 20 --retry 2 -o "$2" "$1"; }
	if get "$url" "$tmp/silesia.tar.zst" && [ "$(sha256 "$tmp/silesia.tar.zst")" = "$zst_sha256" ]; then
		zstd -q -d "$tmp/silesia.tar.zst" -o "$tmp/silesia.tar"
		from=klauspost.com
	else
		echo "klauspost.com failed; rebuilding silesia.tar from $zip_url" >&2
		get "$zip_url" "$tmp/silesia.zip"
		[ "$(sha256 "$tmp/silesia.zip")" = "$zip_sha256" ] || { echo "silesia.zip: SHA-256 mismatch" >&2; exit 1; }
		unzip -q "$tmp/silesia.zip" -d "$tmp/files"
		python3 silesia_tar.py "$tmp/files" "$tmp/silesia.tar"
		from=silesia.zip
	fi
	[ "$(sha256 "$tmp/silesia.tar")" = "$tar_sha256" ] || { echo "silesia.tar: SHA-256 mismatch" >&2; exit 1; }
	mv "$tmp/silesia.tar" .
	echo "fetched silesia.tar (from $from)"
fi

if [ "${1:-}" = "-cli" ]; then
	lz4 -q -f -BD -B4 silesia.tar silesia.tar.B4D.lz4
	lz4 -q -f -BD -B7 silesia.tar silesia.tar.B7D.lz4
	echo "made silesia.tar.B4D.lz4 and silesia.tar.B7D.lz4 with $(lz4 --version 2>&1 | head -1)"
fi
