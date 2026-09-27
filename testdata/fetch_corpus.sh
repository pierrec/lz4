#!/bin/sh
# Build a real-world benchmark corpus for BenchmarkCorpus in DIR: the first
# 128 MiB of each of klauspost/compress's test files, all of Silesia, and C-CLI
# linked-block frames of each (NAME.B7D.lz4 and NAME.B4D.lz4). It is about
# 2.5 GiB and downloads a few GiB, so it is not run in CI. Needs curl, zstd,
# unzip and the lz4 CLI; files already in DIR are kept.
#
# Usage: testdata/fetch_corpus.sh DIR
#        LZ4_CORPUS=DIR go test -run '^$' -bench '^BenchmarkCorpus$' .
set -eu
dir=${1:?usage: fetch_corpus.sh DIR}
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$dir"
cd "$dir"

base=https://klauspost.com/files/compress
for f in cockroach.node1.log apache.log github-ranks-backup.bin gob-stream \
	github-june-2days-2019.json nyc-taxi-data-10M.csv sofia-air-quality-dataset.tar \
	consensus.db.10gb rawstudio-mint14.tar 10gb.tar; do
	[ -s "$f" ] && continue
	# Only the first 128 MiB is kept, so the download stops early.
	curl -fsSL "$base/$f.zst" | zstd -dc 2>/dev/null | head -c 134217728 > "$f.tmp" || true
	[ -s "$f.tmp" ] || { echo "$f: download failed" >&2; exit 1; }
	mv "$f.tmp" "$f"
done

if [ ! -s silesia.dickens ]; then
	"$here/fetch_silesia.sh"
	tmp=$(mktemp -d)
	tar -xf "$here/silesia.tar" -C "$tmp"
	for f in "$tmp"/*; do mv "$f" "silesia.$(basename "$f")"; done
	rm -rf "$tmp"
fi

for f in $(ls | grep -v '\.lz4$'); do
	[ -s "$f.B7D.lz4" ] || lz4 -q -BD -B7 -f "$f" "$f.B7D.lz4"
	[ -s "$f.B4D.lz4" ] || lz4 -q -BD -B4 -f "$f" "$f.B4D.lz4"
done
lz4 --version 2>&1 | head -1
