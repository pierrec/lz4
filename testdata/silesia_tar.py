#!/usr/bin/env python3
"""Rebuild klauspost.com's silesia.tar byte for byte from the 12 files in
silesia.zip, for testdata/fetch_silesia.sh to fall back on when klauspost.com
is unreachable. The member order, mtimes and old-GNU header layout below are
those of that tar; the result is checked against its SHA-256 by the caller.

Usage: silesia_tar.py DIR OUT   (DIR holds the unzipped files)
"""
import os
import sys

# (name, mtime) in the tar's member order.
MEMBERS = [
    ("mr", 1048151546),
    ("ooffice", 1025751600),
    ("mozilla", 1022867428),
    ("dickens", 1018610468),
    ("osdb", 1018544178),
    ("x-ray", 1017918012),
    ("reymont", 1017780012),
    ("nci", 1017775316),
    ("samba", 1017059642),
    ("webster", 1017045558),
    ("sao", 1016926716),
    ("xml", 975624866),
]


def header(name, size, mtime):
    h = bytearray(512)
    h[0:len(name)] = name.encode()
    h[100:108] = b"000644 \x00"  # mode; uid and gid stay NUL
    h[124:136] = b"%011o\x00" % size
    h[136:148] = b"%011o\x00" % mtime
    h[148:156] = b" " * 8  # checksum placeholder
    h[156:157] = b"0"
    h[257:265] = b"ustar  \x00"  # old GNU magic and version
    h[148:156] = b"%07o\x00" % sum(h)
    return bytes(h)


def main():
    src, out = sys.argv[1], sys.argv[2]
    with open(out, "wb") as w:
        for name, mtime in MEMBERS:
            with open(os.path.join(src, name), "rb") as f:
                data = f.read()
            w.write(header(name, len(data), mtime))
            w.write(data)
            w.write(b"\x00" * (-len(data) % 512))
        w.write(b"\x00" * 512)  # a single end-of-archive block


if __name__ == "__main__":
    main()
