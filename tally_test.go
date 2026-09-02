package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func says(t *testing.T, text, want string) {
	t.Helper()
	if !strings.Contains(flat(text), want) {
		t.Errorf("the report does not say %q:\n%s", want, text)
	}
}

func saysNot(t *testing.T, text, want string) {
	t.Helper()
	if strings.Contains(flat(text), want) {
		t.Errorf("the report says %q and should not:\n%s", want, text)
	}
}

// flat squashes the wrapping, because the report is written to a terminal
// width and any phrase worth checking for is split across two lines by the
// time it gets here.
func flat(text string) string { return strings.Join(strings.Fields(text), " ") }

// The finding this program exists for.
func TestTheTwoListingsDisagree(t *testing.T) {
	body := zipOf(t, []made{
		{Name: "readme.txt", AlsoCalled: "innocent.txt", Body: "hello",
			Central: true, Local: true},
		entry("other.txt", "world"),
	}, "", "")

	got := onDisk(t, "twofaced.zip", body)
	text := readOf(t, got, false)
	says(t, text, "the two listings do not agree")
	says(t, text, "is called readme.txt in the other list")

	if got.writeUp(false).worst() != Lies {
		t.Error("an archive that describes itself two ways is the loudest thing here")
	}
}

func TestAFileInOnlyOneListing(t *testing.T) {
	hidden := zipOf(t, []made{
		entry("shown.txt", "a"),
		{Name: "hidden.txt", Body: "b", Local: true},
	}, "", "")
	says(t, readOf(t, onDisk(t, "hidden.zip", hidden), false),
		"in the archive and not in the index")

	ghost := zipOf(t, []made{
		entry("shown.txt", "a"),
		{Name: "ghost.txt", Body: "b", Central: true},
	}, "", "")
	says(t, readOf(t, onDisk(t, "ghost.zip", ghost), false),
		"have no header where the index says they start")
}

func TestSizesThatDisagree(t *testing.T) {
	body := zipOf(t, []made{
		{Name: "thing.bin", Body: "small", Central: true, Local: true, WrongSize: 900000},
	}, "", "")
	says(t, readOf(t, onDisk(t, "sizes.zip", body), false), "in one list and")
}

// An honest archive has to come back quiet or none of the rest is worth
// printing.
func TestAnHonestZipIsQuiet(t *testing.T) {
	body := zipOf(t, []made{
		entry("thing/", ""),
		entry("thing/README.md", "# thing\n"),
		entry("thing/main.go", "package main\n"),
	}, "", "")

	got := onDisk(t, "tidy.zip", body)
	report := got.writeUp(false)
	if report.worst() >= Lands {
		t.Errorf("a tidy archive needed a decision:\n%s", readOf(t, got, false))
	}
	says(t, readOf(t, got, false), "Nothing here will land anywhere it should not")
}

func TestPathsThatLeaveTheFolder(t *testing.T) {
	body := zipOf(t, []made{
		entry("thing/ok.txt", "fine"),
		entry("../../.ssh/authorized_keys", "ssh-rsa AAAA"),
		entry("/etc/cron.d/thing", "* * * * * root /tmp/x"),
	}, "", "")

	text := readOf(t, onDisk(t, "slip.zip", body), false)
	says(t, text, "2 entries land outside the folder")
	says(t, text, "climbs out of the folder")
	says(t, text, "starts at the root")
}

func TestWindowsPathsAreAbsoluteToo(t *testing.T) {
	body := zipOf(t, []made{entry(`C:\Windows\System32\thing.dll`, "x")}, "", "")
	says(t, readOf(t, onDisk(t, "drive.zip", body), false), "starts at the root")
}

func TestTwoNamesThatBecomeOneFile(t *testing.T) {
	body := zipOf(t, []made{
		entry("thing/README.md", "the real one"),
		entry("thing/readme.md", "the other one"),
	}, "", "")
	text := readOf(t, onDisk(t, "case.zip", body), false)
	says(t, text, "becomes one file on Windows and on a Mac")
}

func TestTheSameNameTwice(t *testing.T) {
	body := zipOf(t, []made{
		entry("thing/x.txt", "first"),
		entry("thing/x.txt", "second"),
	}, "", "")
	says(t, readOf(t, onDisk(t, "twice.zip", body), false), "name appears more than once")
}

// The override is the one used in practice, and the report has to print
// the name without it or the finding draws itself backwards too.
func TestANameDrawnBackwards(t *testing.T) {
	body := zipOf(t, []made{
		entry("holiday\u202egnp.exe", "MZ"),
	}, "", "")
	text := readOf(t, onDisk(t, "rtl.zip", body), false)
	says(t, text, "a right to left override")
	if strings.ContainsRune(text, '\u202e') {
		t.Error("the report printed the override itself, so the report draws backwards too")
	}
}

func TestDoubleExtension(t *testing.T) {
	body := zipOf(t, []made{entry("invoice.pdf.exe", "MZ")}, "", "")
	says(t, readOf(t, onDisk(t, "double.zip", body), false), "it ends in .exe behind a .pdf")
}

func TestNamesWindowsWillChange(t *testing.T) {
	for _, name := range []string{"thing/aux.txt", "thing/notes.txt ", "thing/what?.txt"} {
		body := zipOf(t, []made{entry(name, "x")}, "", "")
		text := readOf(t, onDisk(t, "odd.zip", body), false)
		says(t, text, "does not say what it is")
	}
}

func TestZeroWidthInAName(t *testing.T) {
	body := zipOf(t, []made{
		entry("thing/setup.sh", "a"),
		entry("thing/setup\u200b.sh", "b"),
	}, "", "")
	says(t, readOf(t, onDisk(t, "zero.zip", body), false), "a zero width space")
}

// A symlink is harmless on its own. It is the entry after it, written
// through it, that lands somewhere else.
func TestALinkOutOfTheFolder(t *testing.T) {
	body := zipOf(t, []made{
		{Name: "thing/config", Body: "../../../../etc", Mode: 0o120777, Central: true, Local: true},
		entry("thing/config/passwd", "root:x:0:0"),
	}, "", "")

	text := readOf(t, onDisk(t, "link.zip", body), false)
	says(t, text, "1 link points outside the folder")
	says(t, text, "../../../../etc")
}

func TestSomethingInFrontOfTheZip(t *testing.T) {
	body := zipOf(t, []made{entry("thing/x.txt", "x")}, strings.Repeat("MZ junk ", 64), "")
	text := readOf(t, onDisk(t, "prepended.zip", body), false)
	says(t, text, "before the first file")
}

func TestTheArchiveComment(t *testing.T) {
	body := zipOf(t, []made{entry("thing/x.txt", "x")}, "", "a note nobody will ever see")
	says(t, readOf(t, onDisk(t, "comment.zip", body), false), "a note nobody will ever see")
}

func TestFilesThatShouldNotLeaveAMachine(t *testing.T) {
	body := zipOf(t, []made{
		entry("project/src/main.go", "package main"),
		entry("project/.env", "STRIPE_KEY=x"),
		entry("project/deploy/id_rsa", "-----BEGIN"),
	}, "", "")
	text := readOf(t, onDisk(t, "sloppy.zip", body), false)
	says(t, text, "2 files in here should not leave a machine")
	says(t, text, "an environment file")
	says(t, text, "a private ssh key")
}

func TestWhatCameAlong(t *testing.T) {
	body := zipOf(t, []made{
		entry("project/x.txt", "x"),
		entry("__MACOSX/._x.txt", "\x00\x05\x16\x07"),
		entry("project/.DS_Store", "\x00\x00\x00\x01Bud1"),
		entry("project/.git/config", "[core]"),
	}, "", "")
	text := readOf(t, onDisk(t, "messy.zip", body), false)
	says(t, text, "3 things came along that nobody put in")
	says(t, text, "a whole git repository")
}

func TestSpillingIntoTheCurrentFolder(t *testing.T) {
	body := zipOf(t, []made{
		entry("README.md", "a"),
		entry("main.go", "b"),
		entry("go.mod", "c"),
	}, "", "")
	says(t, readOf(t, onDisk(t, "bomb.zip", body), false), "land straight in the folder you are standing in")
}

// One folder holding everything is the tidy case and must not be reported.
func TestOneFolderIsNotASpill(t *testing.T) {
	body := zipOf(t, []made{
		entry("thing-1.0/", ""),
		entry("thing-1.0/README.md", "a"),
		entry("thing-1.0/main.go", "b"),
	}, "", "")
	saysNot(t, readOf(t, onDisk(t, "tidy.zip", body), false), "land straight in the folder")
}

func TestSetuidInAZip(t *testing.T) {
	body := zipOf(t, []made{
		{Name: "thing/helper", Body: "ELF", Mode: 0o104755, Central: true, Local: true},
	}, "", "")
	says(t, readOf(t, onDisk(t, "setuid.zip", body), false), "marked to run as its owner")
}

// --- tar ---

func tarOf(t *testing.T, heads []*tar.Header, zipped bool) []byte {
	t.Helper()
	var raw bytes.Buffer
	w := tar.NewWriter(&raw)
	for _, h := range heads {
		if h.ModTime.IsZero() {
			h.ModTime = time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
		}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			w.Write(bytes.Repeat([]byte("x"), int(h.Size)))
		}
	}
	w.Close()
	if !zipped {
		return raw.Bytes()
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	gz.Write(raw.Bytes())
	gz.Close()
	return out.Bytes()
}

func TestATarKeepsTheNameOfWhoeverMadeIt(t *testing.T) {
	body := tarOf(t, []*tar.Header{
		{Name: "thing/", Typeflag: tar.TypeDir, Mode: 0o755, Uname: "kamat", Gname: "staff", Uid: 501},
		{Name: "thing/README.md", Size: 10, Mode: 0o644, Uname: "kamat", Gname: "staff", Uid: 501},
	}, true)

	got := onDisk(t, "owned.tar.gz", body)
	text := readOf(t, got, false)
	says(t, text, "what the archive says about where it was made")
	says(t, text, "kamat")
	if got.Kind != "a tar, gzip compressed" {
		t.Errorf("it came out as %q", got.Kind)
	}
}

func TestATarWithThingsThatAreNotFiles(t *testing.T) {
	body := tarOf(t, []*tar.Header{
		{Name: "thing/dev/null", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3, Mode: 0o666},
		{Name: "thing/pipe", Typeflag: tar.TypeFifo, Mode: 0o644},
		{Name: "thing/setuid", Typeflag: tar.TypeReg, Mode: 0o4755, Size: 4},
	}, false)

	text := readOf(t, onDisk(t, "odd.tar", body), false)
	says(t, text, "2 entries are not files")
	says(t, text, "is a device node")
	says(t, text, "marked to run as its owner")
}

func TestATarLinkOutOfTheFolder(t *testing.T) {
	body := tarOf(t, []*tar.Header{
		{Name: "thing/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777},
	}, false)
	says(t, readOf(t, onDisk(t, "link.tar", body), false), "link points outside the folder")
}

// The gzip header carries the name the file had before it was compressed,
// which is nobody's idea of a thing they are sending.
func TestTheNameInsideTheGzip(t *testing.T) {
	inner := tarOf(t, []*tar.Header{{Name: "thing/x", Size: 4, Mode: 0o644}}, false)
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	gz.Name = "release-final-FINAL-v2.tar"
	gz.Write(inner)
	gz.Close()

	says(t, readOf(t, onDisk(t, "named.tar.gz", out.Bytes()), false),
		"release-final-FINAL-v2.tar")
}

func TestSomethingEnormousInsideSomethingSmall(t *testing.T) {
	body := tarOf(t, []*tar.Header{
		{Name: "thing/big", Size: 60 << 20, Mode: 0o644},
	}, true)
	says(t, readOf(t, onDisk(t, "bomb.tar.gz", body), false), "once unpacked")
}

func TestListingEverything(t *testing.T) {
	body := zipOf(t, []made{
		entry("thing/a.txt", "a"),
		entry("thing/b.txt", "bb"),
	}, "", "")
	got := onDisk(t, "list.zip", body)
	says(t, readOf(t, got, true), "everything in it")
	saysNot(t, readOf(t, got, false), "everything in it")
}

func TestSomethingThatIsNotAnArchive(t *testing.T) {
	if _, err := look("tally_test.go"); err == nil {
		t.Error("a go file read as an archive")
	}
}

// --- 7z ---

// The check the whole LZMA decoder exists to pass. Two archives of the same
// three files, one with its listing compressed and one without, have to
// come out identical. A decoder that is subtly wrong does not fail here, it
// produces a different list, and that is what this catches.
func TestTheCompressedListingDecodesToTheSameThing(t *testing.T) {
	packed := onDisk(t, "plain.7z", sevenPlain)
	plain := onDisk(t, "nohdr.7z", sevenNoHeader)

	if len(packed.Items) != len(plain.Items) || len(packed.Items) != 5 {
		t.Fatalf("%d entries against %d", len(packed.Items), len(plain.Items))
	}
	for i := range packed.Items {
		if packed.Items[i].Name != plain.Items[i].Name {
			t.Errorf("entry %d is %q in one and %q in the other",
				i, packed.Items[i].Name, plain.Items[i].Name)
		}
		if packed.Items[i].Size != plain.Items[i].Size {
			t.Errorf("%s is %d bytes in one and %d in the other",
				packed.Items[i].Name, packed.Items[i].Size, plain.Items[i].Size)
		}
	}
	if !packed.Packed || plain.Packed {
		t.Error("one of them has a compressed listing and the other does not")
	}
}

// The sizes here are what 7z l prints for the same archive.
func TestSevenIsReadTheWay7zReadsIt(t *testing.T) {
	got := onDisk(t, "plain.7z", sevenPlain)
	want := []struct {
		name  string
		size  uint64
		isDir bool
	}{
		{"pkg", 0, true},
		{"pkg/deploy", 0, true},
		{"pkg/.env", 20, false},
		{"pkg/deploy/big.txt", 5005, false},
		{"pkg/README.md", 9, false},
	}
	if len(got.Items) != len(want) {
		t.Fatalf("got %d entries, wanted %d", len(got.Items), len(want))
	}
	for i, w := range want {
		item := got.Items[i]
		if item.Name != w.name || item.Size != w.size || item.Directory() != w.isDir {
			t.Errorf("entry %d is %q %d dir=%v, wanted %q %d dir=%v",
				i, item.Name, item.Size, item.Directory(), w.name, w.size, w.isDir)
		}
	}
	if got.Kind != "a 7z" {
		t.Errorf("it came out as %q", got.Kind)
	}
}

// An archive with an encrypted listing looks exactly like an empty one, and
// saying which is which is the finding.
func TestSevenThatWillNotSayWhatItHolds(t *testing.T) {
	got := onDisk(t, "locked.7z", sevenLocked)
	text := readOf(t, got, false)
	says(t, text, "it will not say what it contains")
	says(t, text, "is encrypted, not just the files")

	if got.writeUp(false).worst() != Lies {
		t.Error("an archive that refuses to describe itself is the loudest thing here")
	}
	saysNot(t, text, "Nothing here will land anywhere it should not")
}

func TestTheSevenFindsWhatCameAlong(t *testing.T) {
	says(t, readOf(t, onDisk(t, "plain.7z", sevenPlain), false),
		"should not leave a machine")
}

// A truncated 7z must come back rather than reading off the end of it.
func TestABrokenSevenStops(t *testing.T) {
	for _, cut := range []int{40, 100, 200, len(sevenPlain) - 8} {
		body := append([]byte{}, sevenPlain[:cut]...)
		path := filepath.Join(t.TempDir(), "cut.7z")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := look(path); err == nil {
			readOf(t, got, true)
		}
	}
}
