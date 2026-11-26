Test data

The golden data files (*.lz4) have been compressed with the original C
implementation of lz4. Raw inputs are stored gzip-compressed (*.gz).

Sources and licences

Mark.Twain-Tom.Sawyer.txt
    The Adventures of Tom Sawyer, by Mark Twain: an earlier edition of
    Project Gutenberg eBook #74 (https://www.gutenberg.org/ebooks/74), with
    the Project Gutenberg header and licence removed. Public domain in the
    United States.
Mark.Twain-Tom.Sawyer_long.txt
    The same text repeated about eleven times. Mark.Twain-Tom.Sawyer_linked
    is a symbolic link to it, compressed with linked blocks.
Mark.Twain-Tom.Sawyer_legacy.txt.lz4
    Mark.Twain-Tom.Sawyer.txt as a single-block legacy frame, followed by its
    size and other bytes, the way a Linux kernel image stores its payload.
    make_legacy.sh rebuilds it with the C lz4 tool.
pg1661.txt
    The Adventures of Sherlock Holmes, by Arthur Conan Doyle: Project
    Gutenberg eBook #1661 (https://www.gutenberg.org/ebooks/1661), with its
    Project Gutenberg header, under the Project Gutenberg License.
gettysburg.txt
    Abraham Lincoln's Gettysburg Address. Public domain.
e.txt, pi.txt
    Decimal digits of e and pi.
random.data, random_appended.data.lz4, repeat.txt, empty.txt, upperbound.data
    Small inputs added with the tests that use them.
vmlinux_LZ4_19377
    A Linux kernel image as built by Ubuntu: 5.4.0-48.52-generic, compressed
    in the legacy frame format the kernel uses, followed by its size and
    other bytes. It is licensed under the GNU General Public License,
    version 2. Its corresponding source is Ubuntu's linux 5.4.0-48.52 source
    package: https://launchpad.net/ubuntu/+source/linux/5.4.0-48.52
pg_control.tar
    A PostgreSQL pg_control file, added for a bug fix (908ada6).
issue43.data, issue51.data, issue102.data
    Inputs that reproduced the bugs reported in issues #43, #51 and #102.
    Where they came from is not recorded.
malformed.block.lz4
    A frame with a corrupt block.
compress.golden, ccompat.golden
    Digests of this package's and of lz4 1.10.0's compressed output.
silesia.tar
    Not stored here. fetch_silesia.sh downloads the Silesia corpus for the
    tests that use it.
