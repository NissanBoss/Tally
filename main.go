package main

// Tally says what an archive will do to the folder you unpack it into.
//
// At a dock, the tally clerk is the one who counts what comes off the ship
// against what the manifest says is on it. That is the job here. An archive
// is a list of names and a promise about where they go, and both halves are
// written by whoever built it.
//
// The part worth having is the one nothing else does: a zip carries two
// lists of its contents, an index at the end and a header in front of each
// file, and nothing in the format makes them agree. Different programs read
// different halves. So this reads both.
//
// The rest is what happens when a name is not a name: a path that climbs
// out of the folder, a link that points somewhere else and gets written
// through afterwards, two names that are one file on Windows, a name drawn
// backwards so the extension you read is not the one that runs.
//
// It never extracts anything. It reads the listing and the headers, and the
// only bytes it decompresses are the ones inside a symlink, because where a
// link points is the whole question about a link.

import (
	"flag"
	"fmt"
	"os"
)

var version = "unreleased"

const (
	exitQuiet  = 0
	exitDecide = 1 // something in here is a decision, not a fact
	exitBroken = 2
	exitUnread = 3 // part of it could not be read, so nothing here is a pass
)

func main() {
	all := flag.Bool("all", false, "list every entry as well as the findings")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Usage = usage
	flag.Parse()

	if *showVersion {
		fmt.Println("Tally " + version)
		return
	}
	if flag.NArg() == 0 {
		usage()
		os.Exit(exitBroken)
	}

	worst := Note
	unread := false
	for _, name := range flag.Args() {
		t, err := look(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tally: %s: %v\n", name, err)
			os.Exit(exitBroken)
		}
		report := t.writeUp(*all)
		report.render(os.Stdout, t.Name+"  ("+t.summary()+")")
		if report.worst() > worst {
			worst = report.worst()
		}
		if len(report.Gaps) > 0 {
			unread = true
		}
	}

	switch {
	case worst >= Lands:
		os.Exit(exitDecide)
	case unread:
		os.Exit(exitUnread)
	}
	os.Exit(exitQuiet)
}

func usage() {
	fmt.Fprintln(os.Stderr, `Tally - what an archive will do when you unpack it

  tally thing.zip            one archive
  tally *.zip *.tar.gz       several
  tally --all thing.zip      list every entry as well
  tally --version

A zip says what it contains twice: an index at the end, and a header in
front of each file. Nothing in the format makes the two agree, and
different programs read different halves, so an archive can show one set of
files to whatever lists it and hand a different set to whatever unpacks it.

This reads both, and then asks the rest: where will these actually land,
which two names become one file on your machine, which link points out of
the folder, and what came along that nobody chose to send.

Zip, 7z, tar, tar.gz and tar.bz2. It never extracts anything. A 7z keeps its
own list of contents compressed, so reading one means decoding LZMA, and
that is in here too.`)
}
