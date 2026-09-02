# Changelog

## v1

The first one.

Tally reads an archive and says what it will do when you unpack it, without
unpacking any of it.

The finding it exists for is one nothing else will tell you: **a zip lists
its contents twice, in an index at the end and in a header in front of each
file, and nothing in the format makes the two agree.** Different programs
read different halves, so an archive can show one set of files to whatever
lists it and hand a different set to whatever unpacks it. So this reads
both, by hand, because every zip library reads one of them.

Around that:

- paths that climb out of the folder, start at the root, or name a drive
- symlinks pointing outside, and the entry after them that gets written
  through
- two names that are one file on Windows and on a Mac
- names with a right to left override, a zero width character, a double
  extension, or a name Windows will not make
- archives that spill into whatever directory you were standing in
- the `.env`, the `id_rsa` and the `.git` that came along when somebody
  packed a folder rather than a list of files
- setuid bits, device nodes, fifos and hard links
- what unpacks to hundreds of times its own size
- and who made it: the login name and numeric id a tar carries against
  every single entry

Zip, tar, tar.gz and tar.bz2. Anything else is named and said to be unread
rather than counted as clean.

It never extracts anything. The only bytes it decompresses are the ones
inside a symlink entry, because where a link points is the whole question
about a link.

Five packages: Windows, Linux, Linux on ARM, and both kinds of Mac. One
binary each, no dependencies, MIT.

## v1.1

**7z, which meant writing an LZMA decoder.**

Every other format here says what is in it out of plain bytes. A 7z does
not: the list of contents is itself an LZMA stream, so there is no way to
find out what a 7z holds without decoding LZMA. Not the files, just the
names. So there is a decoder in here now, about six hundred lines of range
coder and probability models, and its only job is to read a listing.

It is checked the only way that means anything. The same three files were
packed by 7-Zip twice, once normally and once with the listing left in the
clear, and both archives are in the tests as bytes. The two have to come out
identical. A wrong decoder does not fail, it produces a plausible list of
files that is not the one in the archive, and that is what this catches.

Two findings come with the format and exist nowhere else:

- **An archive that will not say what it contains.** Made with `-mhe=on`, a
  7z encrypts the names as well as the contents, so without the password it
  looks exactly like an empty archive. Nothing tells people which of the two
  they are looking at.
- **Entries that delete.** 7z anti-items exist so an incremental backup can
  record a deletion. An entry whose job is to take a file off your disk
  rather than put one on it, and no listing tool shows the difference.

A rar is still recognised and reported as unread. Its headers are not
compressed and it would be the easier of the two to add, and the program
that writes one is not free software, so there is no honest way to test a
reader against archives it has never been shown.
