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
