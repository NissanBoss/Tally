# Tally

What an archive will do when you unpack it.

At a dock, the tally clerk is the one who counts what comes off the ship
against what the manifest says is on it. That is the job here.

```
    [LIES   ] the two listings do not agree
             invoice.pdf                       is called run.bat in the other list
             1 file is                         in the archive and not in the index, so
                                               a listing will not show it

    [LIES   ] 1 name does not say what it is
             holiday.gnp.exe                   it has a right to left override in it
               the name draws itself in a different order from the one it
               is written in, so what you read is not what lands

    [LANDS  ] 2 entries land outside the folder
             ../../.ssh/authorized_keys        climbs out of the folder you unpack into
             /etc/cron.d/thing                 starts at the root, so it ignores where
                                               you unpack it

    [LANDS  ] 1 link points outside the folder
             pkg/deploy/config                 points at ../../../../etc

    [CARRIES] what the archive says about where it was made
             runner                            4 entries belong to that name
             timestamps                        2 September 2026, all within a minute
                                               of each other
```

## The problem

**A zip says what it contains twice, and nothing makes the two agree.**

At the end of the file there is a central directory: one record per file,
with the name, the sizes, the checksum, and the offset of where that file
starts. At each of those offsets there is a local header, saying the same
things again.

They are written by the same program at the same moment, so in an honest
archive they always match, and the specification never says they have to.

That gap matters because **different programs read different halves.** A
tool that lists an archive reads the central directory, because it is an
index and it is right there at the end. A tool that streams one reads the
local headers, because it is going forwards and those come first. So an
archive can be built to show one set of files to whatever looked at it and
hand a different set to whatever unpacked it, and both programs are doing
exactly what they were written to do.

Nothing you already have will tell you when that has happened. `unzip -l`
prints one of the two lists and does not mention that there is another one.

## What else it asks

Once you are reading the listing properly, the rest of the questions are
right there:

**Where will these actually land.** A path that climbs out with `..`, a
path that starts at the root or names a drive, a symlink pointing outside
the folder with an entry after it that gets written through it. That last
one is how the path checks in several extractors were got round in 2025,
and it is two ordinary looking entries that only do anything together.

**Which two names become one file.** `README.md` and `readme.md` are two
files on the machine that built the archive and one file on Windows and on
a Mac. The second one written wins and nothing warns anybody.

**Whether the names say what they are.** A right to left override turns
`holiday‮gnp.exe` into something that reads as a png and still runs as an
exe. A zero width space makes two names that look identical. `invoice.pdf.exe`
has one extension and it is not the one people read. `aux.txt` is a name
Windows will not make at all.

**Whether it explodes.** Twenty files at the top level go all over whatever
directory you were standing in, and anything with a matching name is
overwritten without a word.

**What came along.** The `.env` and the `id_rsa` somebody swept up when they
packed a folder instead of a list of files. The `.git` directory, which is
the whole history rather than the files. The `__MACOSX` shadow copies and
the `.DS_Store` that lists what used to be in that folder.

**Who made it.** Every entry in a tar carries the login name and the numeric
id of whoever made it, and almost nobody knows it is in there. Point this at
a release tarball built by GitHub Actions and it says `runner`. Point it at
one somebody built at their desk and it says their name.

## The 7z, which hides the list itself

Every other format here answers "what is in this" out of plain bytes. A zip
keeps its index in the clear at the end; a tar announces each file in a plain
block in front of it.

**A 7z compresses the list of contents.** The signature at the front points
at a header, and that header is almost always an encoded header: a small
description of how to decompress the real one, which is an LZMA stream. So
there is no way to answer what is in a 7z without decoding LZMA. Not the
files. Just the list of their names.

So there is an LZMA decoder in here: the range coder, the adaptive
probability models, the length and distance decoders. About six hundred
lines whose only job is to find out what an archive is called inside.

It is checked the only way that means anything. The same three files were
packed twice by 7-Zip, once normally and once with `-mhc=off` so the listing
stays in the clear, and both are in the tests. The two have to come out
saying exactly the same thing. A decoder that is subtly wrong does not fail:
it produces plausible bytes, and plausible bytes here is a list of files that
reads like a list of files and is not the one in the archive.

Two findings come with the format and neither exists anywhere else:

**It can refuse to say.** A 7z made with `-mhe=on` encrypts the names as well
as the contents. Opened without the password it has no list at all, which
looks exactly like an archive with nothing in it. Nothing tells people which
of those two they are looking at, and this does.

**It can delete things.** 7z carries anti-items: entries whose job is to
remove a file when the archive is unpacked, so that an incremental backup can
record a deletion. An entry that takes something off your disk rather than
putting something on it, and no listing tool shows the difference.

## What already exists

`unzip -l` and `tar -t` print one listing and stop. That is what they are
for and it is why the question above has no answer in them.

The tools that do check for these are libraries and hardened extractors for
developers, like [zipguard](https://github.com/Mhacker1020/zipguard): they
enforce a policy while unpacking, inside a program somebody is writing.
There is nothing that gives the person about to double click an archive a
report on it first, and none of them compares the two listings.

The advisories are plentiful. The path traversal in 7-Zip in 2025
([CVE-2025-11001](https://nvd.nist.gov/vuln/detail/CVE-2025-11001)) went
through a symlink, exactly as above.

## How it reads it

The zip reader is written here rather than taken from the standard library,
because every zip library reads one of the two listings and this program is
about both. So it walks the end of central directory record, the zip64
records behind it when the archive is large, every central directory entry,
and then goes back to the front and reads every local header in order.
Comparing those is the whole point and no library will hand you both.

The tar side uses the standard library, because a tar has one list and there
is nothing to compare. What it adds is everything a tar can carry that a zip
cannot: device nodes, fifos, hard links, and the owner on every entry.

The 7z reader and the LZMA decoder behind it are both written here, for the
reason in the section above: there is no other way to find out what a 7z
contains.

**It never extracts anything.** The only bytes it decompresses are the ones
inside a symlink entry, because where a link points is the entire question
about a link.

## What it does not do

It reads the listing, not the files. What is inside each one is a different
question, and an archive inside the archive is named rather than opened.

Zip, 7z, tar, tar.gz and tar.bz2. An xz, a zstd or a rar is recognised and
said to be unread rather than counted as clean. A rar would be the easiest
of those to add, because its headers are not compressed, and it is not here
because the program that writes one is not free software: there is no honest
way to test a reader against archives it has never been shown.

It cannot tell you whether an archive is malicious. A release tarball that
spills into the current directory is untidy, not an attack, and plenty of
honest software ships with a setuid binary in it. What it can do is make
sure that when you decide, you are deciding with the listing open.

## Running it

```
tally thing.zip            one archive
tally *.zip *.tar.gz       several
tally --all thing.zip      list every entry as well as the findings
tally --version
```

Exit codes, for a hook or a pipeline:

| Code | Meaning |
|---|---|
| 0 | nothing in here lands anywhere it should not |
| 1 | something in here is a decision rather than a fact |
| 2 | it could not read what it was given |
| 3 | it read some of it and says which part it could not |

## Building it

```
go build
```

Go 1.26 or newer, nothing else. No dependencies, and a test that fails the
build if one appears.

```
sh build.sh v1
```

builds the five packages the releases are made of.

## The promises

- **It never extracts anything.** Nothing is written, anywhere, ever. A test
  reads the source and fails the build if it learns how.
- **It never runs anything and never opens a socket.**
- **It says what it could not read**, rather than counting silence as a pass.

## Licence

MIT.
