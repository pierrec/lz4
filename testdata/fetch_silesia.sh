#!/bin/sh
# Fetch the Silesia corpus into testdata/silesia.tar, the file name that
# klauspost/compress's tests and benchmarks also read. It holds the corpus's
# 12 files in the usual order, as a reproducible GNU tar (211957760 bytes;
# the widely used 211947520-byte silesia.tar has the same file data, framed
# differently). The download and the result are both checked against pinned
# SHA-256s, so every machine tests the same bytes. Needs curl, unzip and GNU
# tar.
#
# Usage: testdata/fetch_silesia.sh
set -eu

zip_url=https://sun.aei.polsl.pl/~sdeor/corpus/silesia.zip
zip_sha256=0626e25f45c0ffb5dc801f13b7c82a3b75743ba07e3a71835a41e3d9f63c77af
tar_sha256=6e2bc2220fa51f7027518432c7fd01fe3ba830f69ffd8c89622ab073d474cf29

cd "$(dirname "$0")"

sha256() {
	if command -v sha256sum > /dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1
}

if [ -f silesia.tar ] && [ "$(sha256 silesia.tar)" = "$tar_sha256" ]; then
	echo "silesia.tar is up to date"
	exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL --retry 3 -o "$tmp/silesia.zip" "$zip_url"
[ "$(sha256 "$tmp/silesia.zip")" = "$zip_sha256" ] || { echo "silesia.zip: SHA-256 mismatch" >&2; exit 1; }
unzip -q "$tmp/silesia.zip" -d "$tmp/files"
# Fixed order, owner, mode and mtime make the tar reproducible.
(cd "$tmp/files" && tar --format=gnu --owner=0 --group=0 --numeric-owner --mode=0644 --mtime=@0 \
	-cf "$tmp/silesia.tar" dickens mozilla mr nci ooffice osdb reymont samba sao webster x-ray xml)
[ "$(sha256 "$tmp/silesia.tar")" = "$tar_sha256" ] || { echo "silesia.tar: SHA-256 mismatch" >&2; exit 1; }
mv "$tmp/silesia.tar" silesia.tar
echo "fetched silesia.tar"
