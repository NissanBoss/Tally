package main

// Turning an archive into an answer.
//
// The question is what happens if you unpack this, and it comes apart into
// three that get asked in the same order every time:
//
//	does it describe itself honestly
//	where will the files actually land
//	and what is in it that nobody chose to put there
//
// The first one is on top because it decides how much the other two are
// worth. An archive whose two listings disagree has already been shown to
// say one thing and do another, so nothing below that finding can be taken
// at face value, and the report says so rather than carrying on politely.

import (
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

var errUnknown = errors.New("this is not an archive this knows how to read")

// Item is one entry, whichever kind of archive it came out of.
type Item struct {
	Name string
	Size uint64
	// Packed is the size it takes up in the archive.
	Packed uint64
	Kind   string
	Link   string
	Mode   uint32
	When   time.Time
	// Owner is the login name of whoever made it, when the format carries
	// one, with the numeric id beside it.
	Owner string
	UID   int
	// Method is how it was compressed.
	Method    string
	Encrypted bool
}

func (i Item) Directory() bool {
	return i.Kind == "directory" || strings.HasSuffix(i.Name, "/")
}

// Tally is one archive, read.
type Tally struct {
	Name string
	// Kind is what it turned out to be.
	Kind  string
	Items []Item
	// Size is the file on disk, Unpacked is what it comes to.
	Size     int64
	Unpacked uint64
	// Zip is kept whole when it was one, because the two listings only
	// exist there.
	Zip *Zip
	// Seven is kept whole when it was a 7z, because only that format can
	// refuse to say what it holds.
	Seven *Seven
	Made  string
	// Packed says the list of contents was itself compressed, which only
	// a 7z does.
	Packed   bool
	Gaps     []string
	Findings []Finding
}

// look reads whatever was handed over.
func look(name string) (*Tally, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errUnknown
	}

	head := make([]byte, 512)
	n, _ := file.ReadAt(head, 0)
	head = head[:n]

	t := &Tally{Name: name, Size: info.Size()}

	switch wrapper := wrapperOf(head); {
	case looksLikeSeven(head):
		return t, t.fromSeven(file, info.Size())
	case looksLikeZip(head) || endsLikeZip(file, info.Size()):
		return t, t.fromZip(file, info.Size())
	case wrapper == "gzip" || wrapper == "bzip2":
		return t, t.fromTar(file, info.Size(), wrapper)
	case looksLikeTar(head):
		return t, t.fromTar(file, info.Size(), "")
	case wrapper != "":
		return nil, fmt.Errorf("this is %s and only zip, 7z, tar, tar.gz and tar.bz2 are read here", wrapper)
	}
	return nil, errUnknown
}

// endsLikeZip catches the zip with something stuck on the front of it,
// which does not start with a local header and is still a zip to every
// program that opens one.
func endsLikeZip(file *os.File, size int64) bool {
	_, _, err := findEOCD(file, size)
	return err == nil
}

func (t *Tally) fromZip(file *os.File, size int64) error {
	z, err := ReadZip(file, size)
	if err != nil {
		return err
	}
	t.Zip = z
	t.Kind = "a zip"
	t.Gaps = append(t.Gaps, z.Gaps...)

	for _, e := range z.Central {
		item := Item{
			Name: e.Name, Size: e.Size, Packed: e.Packed, When: e.When,
			Mode: e.Mode, Method: methodName(e.Method), Encrypted: e.Encrypted(),
			Kind: "file",
		}
		switch {
		case e.Symlink():
			item.Kind = "symlink"
			if target, ok := linkTarget(file, e); ok {
				item.Link = target
			}
		case e.Directory():
			item.Kind = "directory"
		}
		if uid, _, found := unixExtra(e.Extra); found {
			item.UID = uid
		}
		t.Items = append(t.Items, item)
		t.Unpacked += e.Size
	}
	if len(z.Central) > 0 {
		t.Made = madeOnName(z.Central[0].MadeOn)
	}
	return nil
}

func (t *Tally) fromSeven(file *os.File, size int64) error {
	s, err := ReadSeven(file, size)
	if s != nil {
		t.Seven = s
		t.Kind = "a 7z"
		t.Gaps = append(t.Gaps, s.Gaps...)
	}
	if errors.Is(err, errLocked7z) {
		// Not a failure to read. The archive read correctly and what it
		// says is that it will not tell you.
		return nil
	}
	if err != nil {
		return err
	}

	for _, f := range s.Files {
		item := Item{
			Name: f.Name, Size: f.Size, When: f.When,
			Mode: f.unixMode(), Method: s.Method, Encrypted: s.DataLocked,
			Kind: "file",
		}
		switch {
		case f.Anti:
			item.Kind = "deletion"
		case f.Symlink():
			item.Kind = "symlink"
		case f.IsDir:
			item.Kind = "directory"
		}
		t.Items = append(t.Items, item)
		t.Unpacked += f.Size
	}
	if s.HeaderPacked {
		t.Packed = true
	}
	return nil
}

func (t *Tally) fromTar(file *os.File, size int64, wrapper string) error {
	tr, err := ReadTar(file, size, wrapper)
	if err != nil {
		return err
	}
	t.Kind = "a tar"
	if wrapper != "" {
		t.Kind = "a tar, " + wrapper + " compressed"
	}
	t.Gaps = append(t.Gaps, tr.Gaps...)

	for _, p := range tr.Pieces {
		// A pax header is metadata about the entry that follows it, not an
		// entry of its own. Every tarball GitHub makes starts with one, and
		// counting it as a file makes a tidy archive look like it spills.
		if p.Kind == `type "g"` || p.Kind == `type "x"` {
			continue
		}
		t.Items = append(t.Items, Item{
			Name: p.Name, Size: uint64(p.Size), Kind: p.Kind, Link: p.Link,
			Mode: uint32(p.Mode), When: p.When, Owner: p.User, UID: p.UID,
		})
		t.Unpacked += uint64(p.Size)
	}
	if tr.Wrapped != "" {
		t.Made = "gzip, wrapping a file called " + tr.Wrapped
	}
	return nil
}

func (t *Tally) writeUp(all bool) *Report {
	r := &Report{}
	for _, g := range t.Gaps {
		if strings.TrimSpace(g) != "" {
			r.gap(g)
		}
	}

	t.refusesToSay(r)
	t.listingsDisagree(r)
	t.namesThatLie(r)
	t.landsElsewhere(r)
	t.collides(r)
	t.spills(r)
	t.linksOut(r)
	t.deletions(r)
	t.strangeKinds(r)
	t.permissions(r)
	t.shouldNotBeHere(r)
	t.leftovers(r)
	t.weight(r)
	t.whoMadeIt(r)
	t.inventory(r, all)

	if r.worst() < Lands {
		r.add(Finding{
			Severity: Note,
			Title:    "Nothing here will land anywhere it should not",
			Advice: "This reads the listing, not the files. What is inside each " +
				"one is a different question, and an archive inside this one is " +
				"only named rather than opened.",
		})
	}
	return r
}

// listingsDisagree is the finding this program exists for.
func (t *Tally) listingsDisagree(r *Report) {
	if t.Zip == nil {
		return
	}
	byOffset := map[uint64]Entry{}
	for _, e := range t.Zip.Local {
		byOffset[e.At] = e
	}

	var rows group
	missing := 0
	extra := 0

	for _, c := range t.Zip.Central {
		local, found := byOffset[c.At]
		if !found {
			missing++
			continue
		}
		switch {
		case local.Name != c.Name:
			rows.add(shorten(c.Name), "is called "+shorten(local.Name)+" in the other list")
		case local.CRC != c.CRC && local.Flags&8 == 0:
			rows.add(shorten(c.Name), "has a different checksum in each list")
		case local.Size != c.Size && local.Flags&8 == 0:
			rows.add(shorten(c.Name), fmt.Sprintf("is %s in one list and %s in the other",
				size(int64(c.Size)), size(int64(local.Size))))
		case local.Method != c.Method:
			rows.add(shorten(c.Name), "is packed one way in one list and another way in the other")
		}
	}
	for range t.Zip.Local {
		extra++
	}
	extra -= len(t.Zip.Central)

	if rows.count == 0 && missing == 0 && extra <= 0 {
		return
	}
	if missing > 0 {
		rows.add(plural(missing, "file is", "files are"),
			"in the index and have no header where the index says they start")
	}
	if extra > 0 {
		rows.add(plural(extra, "file is", "files are"),
			"in the archive and not in the index, so a listing will not show them")
	}

	r.add(Finding{
		Severity: Lies,
		Title:    "the two listings do not agree",
		Lines:    atMost(rows.lines, 16),
		Advice: "A zip says what it contains twice: once in the index at the end " +
			"and once in front of each file. Nothing makes them match, and " +
			"different programs read different halves, so an archive can show " +
			"one set of files to whatever lists it and hand a different set to " +
			"whatever unpacks it. An honest archive written by an honest tool " +
			"never does this.",
	})
}

func (t *Tally) namesThatLie(r *Report) {
	var rows group
	for _, item := range t.Items {
		for _, odd := range oddities(item.Name) {
			rows.add(shorten(item.Name), odd.What)
			rows.lines = append(rows.lines, folded("  ", odd.Why)...)
			break
		}
		if rows.count >= 12 {
			break
		}
	}
	if rows.count == 0 {
		return
	}
	r.add(Finding{
		Severity: Lies,
		Title:    plural(rows.count, "name does not say what it is", "names do not say what they are"),
		Lines:    atMost(rows.lines, 24),
		Advice: "The name is the only part of an entry anybody reads before " +
			"deciding, and it is entirely the archive's to choose. None of " +
			"these is something a person types by accident.",
	})
}

func (t *Tally) landsElsewhere(r *Report) {
	var out group
	for _, item := range t.Items {
		l := where(item.Name)
		switch {
		case l.Absolute:
			out.add(shorten(item.Name), "starts at the root, so it ignores where you unpack it")
		case l.Escapes:
			out.add(shorten(item.Name), "climbs out of the folder you unpack into")
		}
	}
	if out.count == 0 {
		return
	}
	r.add(Finding{
		Severity: Lands,
		Title:    plural(out.count, "entry lands", "entries land") + " outside the folder",
		Lines:    atMost(out.lines, 16),
		Advice: "Whatever you unpack this into, these do not go in it. Good " +
			"extractors refuse them and plenty of code written in an afternoon " +
			"does not, which is why the same bug has been found in one library " +
			"after another for twenty years.",
	})
}

func (t *Tally) collides(r *Report) {
	seen := map[string]string{}
	var same group
	var caseOnly group

	for _, item := range t.Items {
		if item.Directory() {
			continue
		}
		key := clash(item.Name)
		if first, found := seen[key]; found {
			if first == item.Name {
				same.add(shorten(item.Name), "is in the archive twice")
			} else {
				caseOnly.add(shorten(first), "and "+shorten(item.Name))
			}
			continue
		}
		seen[key] = item.Name
	}

	if same.count > 0 {
		r.add(Finding{
			Severity: Lands,
			Title:    plural(same.count, "name appears", "names appear") + " more than once",
			Lines:    atMost(same.lines, 12),
			Advice: "Which one you end up with depends on the order your " +
				"extractor writes them in, and the one you see in a listing is " +
				"not necessarily the one that survives.",
		})
	}
	if caseOnly.count > 0 {
		r.add(Finding{
			Severity: Lands,
			Title: plural(caseOnly.count, "pair of names becomes one file",
				"pairs of names become one file") + " on Windows and on a Mac",
			Lines: atMost(caseOnly.lines, 12),
			Advice: "Two different files on the machine that built this, one file " +
				"on most of the machines that will open it. The second one " +
				"written wins, and nothing warns anybody.",
		})
	}
}

func (t *Tally) spills(r *Report) {
	loose, top := spillsOut(t.Items)
	if loose < 2 {
		return
	}
	r.add(Finding{
		Severity: Lands,
		Title:    plural(loose, "file lands", "files land") + " straight in the folder you are standing in",
		Advice: "There is no single folder inside this to hold it. Unpacking it " +
			"where you are scatters it over whatever is already there, and " +
			"anything with a matching name is overwritten without a word." +
			ifTop(top),
	})
}

func ifTop(top string) string {
	if top == "" {
		return ""
	}
	return " Most of the rest is under " + top + "."
}

func (t *Tally) linksOut(r *Report) {
	var rows group
	for _, item := range t.Items {
		if item.Kind != "symlink" && item.Kind != "hard link" {
			continue
		}
		if item.Link == "" {
			rows.add(shorten(item.Name), "is a link and this could not read where it points")
			continue
		}
		if pointsOut(item.Name, item.Link) {
			rows.add(shorten(item.Name), "points at "+shorten(item.Link))
		}
	}
	if rows.count == 0 {
		return
	}
	r.add(Finding{
		Severity: Lands,
		Title:    plural(rows.count, "link points", "links point") + " outside the folder",
		Lines:    atMost(rows.lines, 12),
		Advice: "The link itself is harmless. What matters is the entry that " +
			"comes after it and gets written through it, which lands wherever " +
			"the link pointed. That is two ordinary looking entries that only " +
			"do anything together, and it is how the path checks in several " +
			"extractors were got round in 2025.",
	})
}

func (t *Tally) strangeKinds(r *Report) {
	var rows group
	for _, item := range t.Items {
		switch item.Kind {
		case "character device", "block device":
			rows.add(shorten(item.Name), "is a device node")
		case "fifo":
			rows.add(shorten(item.Name), "is a fifo")
		case "hard link":
			if !pointsOut(item.Name, item.Link) {
				rows.add(shorten(item.Name), "is a hard link to "+shorten(item.Link))
			}
		}
	}
	if rows.count == 0 {
		return
	}
	r.add(Finding{
		Severity: Carries,
		Title:    plural(rows.count, "entry is not a file", "entries are not files"),
		Lines:    atMost(rows.lines, 12),
		Advice: "A tar can carry things that are not files at all. Unpacking one " +
			"as root makes it for real, and a device node made in the wrong " +
			"place is a way to read a disk that nobody should have.",
	})
}

func (t *Tally) permissions(r *Report) {
	var rows group
	execs := 0
	for _, item := range t.Items {
		if item.Directory() || item.Mode == 0 {
			continue
		}
		switch {
		case item.Mode&0o4000 != 0:
			rows.add(shorten(item.Name), "runs as whoever owns it")
		case item.Mode&0o2000 != 0:
			rows.add(shorten(item.Name), "runs as whatever group owns it")
		case item.Mode&0o111 != 0:
			execs++
		}
	}
	if rows.count > 0 {
		r.add(Finding{
			Severity: Lands,
			Title:    plural(rows.count, "file is", "files are") + " marked to run as its owner",
			Lines:    atMost(rows.lines, 12),
			Advice: "Unpacked by root, that bit is kept, and anybody who can start " +
				"the file then has root while it runs. It is the oldest way in " +
				"there is and an archive is a strange place to find one.",
		})
	}
	if execs > 0 {
		r.add(Finding{
			Severity: Note,
			Title:    plural(execs, "file is", "files are") + " marked as a program",
			Advice: "Ordinary in a release and worth knowing. A zip made on Windows " +
				"carries no permissions at all, so an archive from there has " +
				"nothing to say here either way.",
		})
	}
}

func (t *Tally) shouldNotBeHere(r *Report) {
	var rows group
	for _, item := range t.Items {
		if place, ok := named(grave, item.Name); ok {
			rows.add(shorten(item.Name), place.What)
			rows.lines = append(rows.lines, folded("  ", place.Why)...)
		}
	}
	if rows.count == 0 {
		return
	}
	r.add(Finding{
		Severity: Carries,
		Title:    plural(rows.count, "file in here", "files in here") + " should not leave a machine",
		Lines:    atMost(rows.lines, 20),
		Advice: "Somebody packed a folder rather than a list of files. Whoever " +
			"has the archive has these, and an archive gets forwarded more " +
			"easily than anything else.",
	})
}

func (t *Tally) leftovers(r *Report) {
	var rows group
	nested := 0
	for _, item := range t.Items {
		if place, ok := named(tagalong, item.Name); ok {
			rows.add(place.What, place.Why)
			continue
		}
		if isNested(item.Name) {
			nested++
		}
	}
	if rows.count > 0 {
		r.add(Finding{
			Severity: Carries,
			Title:    plural(rows.count, "thing came along", "things came along") + " that nobody put in",
			Lines:    atMost(rows.lines, 16),
			Advice: "None of it is a credential. It is the machine that made the " +
				"archive showing through, and some of it lists files that were " +
				"deleted before the archive was made.",
		})
	}
	if nested > 0 {
		r.add(Finding{
			Severity: Note,
			Title:    plural(nested, "entry is itself an archive", "entries are themselves archives"),
			Advice: "This reads one layer. What is inside those is the same set of " +
				"questions again and nothing here has asked them.",
		})
	}
}

func (t *Tally) weight(r *Report) {
	if t.Size == 0 || t.Unpacked == 0 {
		return
	}
	ratio := float64(t.Unpacked) / float64(t.Size)
	if ratio < 200 && t.Unpacked < 4<<30 {
		return
	}
	r.add(Finding{
		Severity: Lands,
		Title: fmt.Sprintf("it is %s on disk and %s once unpacked",
			size(t.Size), size(int64(t.Unpacked))),
		Lines: columns("that is", fmt.Sprintf("%.0f times bigger", ratio)),
		Advice: "Compression does that honestly for some things and a file made " +
			"of one repeated byte does it on purpose. Either way it is worth " +
			"knowing before it goes on a disk that has less room than that.",
	})
}

func (t *Tally) whoMadeIt(r *Report) {
	var rows group

	owners := map[string]int{}
	ids := map[int]bool{}
	for _, item := range t.Items {
		if item.Owner != "" {
			owners[item.Owner]++
		}
		if item.UID > 0 {
			ids[item.UID] = true
		}
	}
	names := make([]string, 0, len(owners))
	for name := range owners {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rows.add(name, plural(owners[name], "entry", "entries")+" belong to that name")
	}
	if len(owners) == 0 && len(ids) > 0 {
		rows.add(plural(len(ids), "numeric owner", "numeric owners"),
			"and no names, so it was packed without them")
	}
	if t.Made != "" {
		rows.add("packed on", t.Made)
	}

	if when, spread := whenMade(t.Items); !when.IsZero() {
		rows.add("timestamps", when.Format("2 January 2006")+spread)
	}
	if rows.count == 0 {
		return
	}
	advice := "A zip made on Windows carries none of this and one made on unix " +
		"carries the numeric owner in an extra field nobody reads. Either way " +
		"it is not a secret and it goes wherever the file goes."
	if t.Zip == nil {
		advice = "A tar keeps the login name and the numeric id of whoever made " +
			"it against every entry, and almost nobody knows it is in there. It " +
			"is not a secret and it is a name that goes wherever the file goes."
	}
	r.add(Finding{
		Severity: Carries,
		Title:    "what the archive says about where it was made",
		Lines:    atMost(rows.lines, 12),
		Advice:   advice,
	})
}

// because every entry sharing a second means a tool set them and a spread
// of years means somebody packed a folder they had been working in.
func whenMade(items []Item) (time.Time, string) {
	var newest, oldest time.Time
	for _, item := range items {
		if item.When.IsZero() {
			continue
		}
		if newest.IsZero() || item.When.After(newest) {
			newest = item.When
		}
		if oldest.IsZero() || item.When.Before(oldest) {
			oldest = item.When
		}
	}
	if newest.IsZero() {
		return newest, ""
	}
	switch gap := newest.Sub(oldest); {
	case gap < time.Minute:
		return newest, ", all within a minute of each other"
	case gap > 365*24*time.Hour:
		return newest, ", spread over " + plural(int(gap.Hours()/24/365), "year", "years")
	}
	return newest, ""
}

func (t *Tally) inventory(r *Report, all bool) {
	files, dirs, links := 0, 0, 0
	for _, item := range t.Items {
		switch {
		case item.Directory():
			dirs++
		case item.Kind == "symlink" || item.Kind == "hard link":
			links++
		default:
			files++
		}
	}

	lines := columns("what it is", t.Kind)
	lines = append(lines, columns("on disk", size(t.Size))...)
	lines = append(lines, columns("unpacked", size(int64(t.Unpacked)))...)
	what := plural(files, "file", "files")
	if dirs > 0 {
		what += ", " + plural(dirs, "directory", "directories")
	}
	if links > 0 {
		what += ", " + plural(links, "link", "links")
	}
	lines = append(lines, columns("holding", what)...)

	if t.Packed {
		lines = append(lines, columns("the listing itself",
			"was compressed, so reading it meant decoding LZMA")...)
	}
	if t.Zip != nil {
		if t.Zip.Before > 0 {
			lines = append(lines, columns("before the first file",
				size(int64(t.Zip.Before))+" of something else")...)
		}
		if t.Zip.Comment != "" {
			lines = append(lines, columns("archive comment", shorten(t.Zip.Comment))...)
		}
		if locked := encryptedCount(t.Items); locked > 0 {
			lines = append(lines, columns("locked",
				plural(locked, "entry needs a password", "entries need a password"))...)
		}
	}
	r.add(Finding{Severity: Note, Title: "what was read", Lines: lines})

	if all {
		r.add(Finding{Severity: Note, Title: "everything in it", Lines: t.everything()})
	}
}

func (t *Tally) everything() []string {
	var out []string
	for i, item := range t.Items {
		if i == 500 {
			out = append(out, fmt.Sprintf("... and %d more", len(t.Items)-i))
			break
		}
		what := size(int64(item.Size))
		if item.Directory() {
			what = "directory"
		}
		if item.Link != "" {
			what = "-> " + shorten(item.Link)
		}
		out = append(out, columns(shorten(item.Name), what)...)
	}
	return out
}

func encryptedCount(items []Item) int {
	n := 0
	for _, item := range items {
		if item.Encrypted {
			n++
		}
	}
	return n
}

// shorten keeps a path inside its column without cutting off the end,
// which is the half that says what the thing is.
func shorten(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || drawing[r] != "" || invisible[r] != "" {
			return -1
		}
		return r
	}, name)
	if len(name) <= 46 {
		return name
	}
	base := path.Base(name)
	if len(base) >= 40 {
		return "..." + base[len(base)-40:]
	}
	return name[:44-len(base)] + ".../" + base
}

func size(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f kB", float64(bytes)/(1<<10))
	case bytes == 1:
		return "1 byte"
	}
	return fmt.Sprintf("%d bytes", bytes)
}

func (t *Tally) summary() string {
	parts := []string{t.Kind, plural(len(t.Items), "entry", "entries")}
	return strings.Join(parts, ", ")
}

func (t *Tally) worth(r *Report) bool { return r.worst() >= Lands }

// refusesToSay is the 7z that will not tell you what it holds.
//
// A 7z can be built so the names are encrypted as well as the contents. Open
// it without the password and there is no list at all, which looks exactly
// like an archive with nothing in it. Saying which of those two you are
// looking at is worth more than anything else this could say about it.
func (t *Tally) refusesToSay(r *Report) {
	if t.Seven == nil || !t.Seven.Locked {
		return
	}
	r.add(Finding{
		Severity: Lies,
		Title:    "it will not say what it contains",
		Lines:    columns("the listing", "is encrypted, not just the files"),
		Advice: "The names, the sizes and the number of files are all behind the " +
			"password here, so nothing at all can be checked before it is " +
			"opened, and an archive like this is indistinguishable from an " +
			"empty one until somebody types the password. Whatever you were " +
			"going to decide by looking at the contents, you cannot.",
	})
}

// deletions are entries whose job is to remove a file rather than write
// one, which 7z carries for incremental backups and which almost nobody
// knows the format can do.
func (t *Tally) deletions(r *Report) {
	var rows group
	for _, item := range t.Items {
		if item.Kind == "deletion" {
			rows.add(shorten(item.Name), "is an instruction to delete that file")
		}
	}
	if rows.count == 0 {
		return
	}
	r.add(Finding{
		Severity: Lands,
		Title:    plural(rows.count, "entry takes", "entries take") + " something off your disk",
		Lines:    atMost(rows.lines, 12),
		Advice: "7z calls these anti-items and they exist so an incremental " +
			"backup can record a deletion. Unpacking this does not only add " +
			"files, it removes one, and no listing tool shows the difference " +
			"between an entry that writes and an entry that deletes.",
	})
}
