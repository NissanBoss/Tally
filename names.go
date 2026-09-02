package main

// What a name in an archive can be, as opposed to what it looks like.
//
// The name is the only part of an entry that reaches the person deciding
// whether to open it, and it is completely under the control of whoever
// built the archive. It does not have to be a name at all: it can be a
// path that climbs out of the folder, a path that starts at the root, a
// name that draws itself backwards, a name that collides with another one
// on your filesystem and not on theirs, or a name your operating system
// will quietly change before it writes it.

import (
	"path"
	"strings"
	"unicode"
)

// Landing is where an entry will actually end up, and how sure that is.
type Landing struct {
	// Clean is the path after the tidying an extractor would do.
	Clean string
	// Escapes is true when it goes above the folder it is unpacked into.
	Escapes bool
	// Absolute is true when the path starts at the root or names a drive.
	Absolute bool
	// Depth is how many directories deep it sits, with 0 meaning it lands
	// straight in the folder you are standing in.
	Depth int
}

// where works out what an extractor would make of a name.
func where(name string) Landing {
	l := Landing{Clean: name}
	n := strings.ReplaceAll(name, "\\", "/")

	// A drive letter or a UNC path is absolute on Windows and reads as an
	// ordinary relative name everywhere else, which is the point of using
	// one.
	if strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") {
		l.Absolute = true
	}
	if len(n) >= 2 && n[1] == ':' && isLetter(n[0]) {
		l.Absolute = true
	}

	depth := 0
	lowest := 0
	for _, part := range strings.Split(strings.TrimSuffix(n, "/"), "/") {
		switch part {
		case "", ".":
		case "..":
			depth--
			if depth < lowest {
				lowest = depth
			}
		default:
			depth++
		}
	}
	l.Escapes = lowest < 0
	l.Depth = depth
	l.Clean = path.Clean(strings.TrimPrefix(n, "/"))
	return l
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// Odd is one thing wrong with a name.
type Odd struct {
	What string
	Why  string
}

// The characters that make a name draw itself differently from the order
// its bytes are in. The override is the one used in practice: it turns
// "report\u202Efdp.exe" into something that reads "reportexe.pdf" on the
// screen and still ends in .exe when it lands.
//
// Written as escapes rather than as themselves, because a file with these
// in it renders wrong in every editor that opens it, including this one.
var drawing = map[rune]string{
	'\u202a': "a left to right embedding",
	'\u202b': "a right to left embedding",
	'\u202c': "a pop directional formatting",
	'\u202d': "a left to right override",
	'\u202e': "a right to left override",
	'\u2066': "a left to right isolate",
	'\u2067': "a right to left isolate",
	'\u2068': "a first strong isolate",
	'\u2069': "a pop directional isolate",
	'\u200e': "a left to right mark",
	'\u200f': "a right to left mark",
}

var invisible = map[rune]string{
	'\u200b': "a zero width space",
	'\u200c': "a zero width non joiner",
	'\u200d': "a zero width joiner",
	'\ufeff': "a byte order mark in the middle of the name",
	'\u00ad': "a soft hyphen",
	'\u2800': "a braille blank",
	'\u3164': "a hangul filler",
	'\u180e': "a mongolian vowel separator",
}

// The names Windows will not take, whatever the extension is after them.
var reserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// The extensions that run when they are opened, for the double extension
// check. A file called holiday.jpg.exe is one file with one extension and
// it is not the one people read.
var runs = map[string]bool{
	".exe": true, ".scr": true, ".com": true, ".pif": true, ".bat": true,
	".cmd": true, ".ps1": true, ".vbs": true, ".vbe": true, ".js": true,
	".jse": true, ".wsf": true, ".wsh": true, ".hta": true, ".msi": true,
	".jar": true, ".apk": true, ".app": true, ".command": true, ".sh": true,
	".lnk": true, ".url": true, ".desktop": true, ".reg": true, ".msc": true,
	".dll": true, ".scpt": true, ".ipa": true, ".deb": true, ".rpm": true,
	".pkg": true, ".dmg": true,
}

// The extensions people read as documents, which is the half of a double
// extension that gets seen.
var reads = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".txt": true, ".rtf": true, ".csv": true,
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".mp3": true,
	".mp4": true, ".zip": true, ".odt": true, ".ods": true, ".webp": true,
}

// oddities is everything wrong with one name.
func oddities(name string) []Odd {
	var out []Odd
	seen := map[string]bool{}
	add := func(what, why string) {
		if seen[what] {
			return
		}
		seen[what] = true
		out = append(out, Odd{what, why})
	}

	for _, r := range name {
		if which, found := drawing[r]; found {
			add("it has "+which+" in it",
				"the name draws itself in a different order from the one it "+
					"is written in, so what you read is not what lands")
		}
		if which, found := invisible[r]; found {
			add("it has "+which+" in it",
				"a character with no width, so two names that look the same "+
					"are two different files")
		}
		if r < 0x20 || r == 0x7f {
			add("it has a control character in it",
				"not something a person types, and it can cut the name short "+
					"in whatever prints it")
		}
		if unicode.Is(unicode.Cf, r) && drawing[r] == "" && invisible[r] == "" {
			add("it has a formatting character in it",
				"a character that changes how the rest is drawn rather than "+
					"standing for anything itself")
		}
	}

	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	lower := strings.ToLower(base)

	if ext := path.Ext(lower); runs[ext] {
		rest := strings.TrimSuffix(lower, ext)
		if inner := path.Ext(rest); reads[inner] {
			add("it ends in "+ext+" behind a "+inner,
				"the extension people read is the middle one and the one that "+
					"decides what happens is the last")
		}
	}

	if stem := strings.TrimSuffix(lower, path.Ext(lower)); reserved[stem] {
		add("it is a name Windows keeps for a device",
			"Windows will not make a file with this name, so whatever unpacks "+
				"it either fails here or writes it somewhere else")
	}
	if strings.HasSuffix(base, " ") || strings.HasSuffix(base, ".") {
		add("it ends in a space or a dot",
			"Windows takes those off, so the file that lands has a different "+
				"name from the one in the archive")
	}
	if len(name) > 200 {
		add("it is longer than most paths are allowed to be",
			"over the limit once it is joined to wherever you unpack it, and "+
				"what happens then is up to the tool")
	}
	if strings.ContainsAny(base, "<>:\"|?*") {
		add("it has characters Windows will not take",
			"the file either fails to write or gets a different name")
	}
	return out
}

// clash is what two names that are different in the archive and the same
// on a filesystem come down to.
func clash(name string) string {
	n := strings.ToLower(strings.ReplaceAll(name, "\\", "/"))
	return strings.TrimSuffix(n, "/")
}

// spillsOut counts what lands straight in the folder you are standing in.
// A single directory that everything else sits under is the tidy case and
// is not a spill, so directories name a top rather than counting as loose.
func spillsOut(items []Item) (loose int, top string) {
	tops := map[string]bool{}
	for _, item := range items {
		n := strings.TrimSuffix(strings.TrimPrefix(
			strings.ReplaceAll(item.Name, "\\", "/"), "./"), "/")
		if n == "" {
			continue
		}
		first, rest, deeper := strings.Cut(n, "/")
		if deeper && rest != "" {
			tops[first] = true
			continue
		}
		if item.Directory() {
			tops[first] = true
			continue
		}
		loose++
	}
	if len(tops) == 1 {
		for name := range tops {
			top = name
		}
	}
	return loose, top
}

// pointsOut says whether a link target leaves the folder it is unpacked
// into. This is the half of a symlink attack that matters: the link itself
// is harmless and what gets written through it afterwards is not.
func pointsOut(from, target string) bool {
	t := strings.ReplaceAll(target, "\\", "/")
	if strings.HasPrefix(t, "/") {
		return true
	}
	if len(t) >= 2 && t[1] == ':' && isLetter(t[0]) {
		return true
	}
	joined := path.Join(path.Dir(strings.ReplaceAll(from, "\\", "/")), t)
	return joined == ".." || strings.HasPrefix(joined, "../")
}
