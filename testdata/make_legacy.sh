#!/bin/bash
# Builds Mark.Twain-Tom.Sawyer_legacy.txt.lz4: a single-block legacy
# frame from the C lz4 tool, followed, like the LZ4 payload of a Linux bzImage,
# by the uncompressed size (little-endian uint32) and other bytes.
set -euo pipefail
LZ4=${LZ4:-lz4}
cd "$(dirname "$0")"
raw=$(mktemp)
trap 'rm -f "$raw"' EXIT
cp Mark.Twain-Tom.Sawyer.txt "$raw"
out=Mark.Twain-Tom.Sawyer_legacy.txt.lz4
"$LZ4" -q -l -9 -c "$raw" > "$out"
size=$(stat -c %s "$raw")
python3 -c "import struct,sys; sys.stdout.buffer.write(struct.pack('<I', $size))" >> "$out"
head -c 4096 "$raw" >> "$out"
ln -sf Mark.Twain-Tom.Sawyer.txt Mark.Twain-Tom.Sawyer_legacy.txt
