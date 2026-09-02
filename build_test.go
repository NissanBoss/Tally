package main

// Building zips byte by byte, because the interesting ones cannot be made
// with a zip library.
//
// Every library writes the two listings from the same values, which is the
// correct thing for a library to do and means it can never produce the
// archive this program exists to find. So the headers are laid out here by
// hand, which has the useful side effect of testing the reader against
// bytes chosen rather than bytes some other program happened to write.

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type made struct {
	// Name is what goes in the local header.
	Name string
	// AlsoCalled, when set, is what goes in the central directory instead,
	// which is the whole trick.
	AlsoCalled string
	Body       string
	// Mode is the unix mode, which makes the entry a symlink or gives it
	// the bits that matter.
	Mode uint32
	// Central and Local say whether the entry appears in each listing.
	Central bool
	Local   bool
	// Comment is the per entry comment.
	Comment string
	// WrongSize puts a different length in the central directory.
	WrongSize uint64
}

func entry(name, body string) made {
	return made{Name: name, Body: body, Central: true, Local: true}
}

// zipOf lays out a whole archive: every local header and its data, then the
// central directory, then the end record.
func zipOf(t *testing.T, entries []made, before, comment string) []byte {
	t.Helper()
	var out bytes.Buffer
	out.WriteString(before)

	type placed struct {
		made
		at     uint64
		crc    uint32
		packed uint64
	}
	var done []placed

	for _, e := range entries {
		at := uint64(out.Len())
		body := []byte(e.Body)
		crc := crc32.ChecksumIEEE(body)

		if e.Local {
			head := make([]byte, 30)
			binary.LittleEndian.PutUint32(head, sigLocal)
			binary.LittleEndian.PutUint16(head[4:], 20)
			binary.LittleEndian.PutUint16(head[8:], 0) // stored
			binary.LittleEndian.PutUint32(head[10:], dosOf(time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)))
			binary.LittleEndian.PutUint32(head[14:], crc)
			binary.LittleEndian.PutUint32(head[18:], uint32(len(body)))
			binary.LittleEndian.PutUint32(head[22:], uint32(len(body)))
			binary.LittleEndian.PutUint16(head[26:], uint16(len(e.Name)))
			out.Write(head)
			out.WriteString(e.Name)
			out.Write(body)
		}
		done = append(done, placed{e, at, crc, uint64(len(body))})
	}

	dirAt := out.Len()
	count := 0
	for _, e := range done {
		if !e.Central {
			continue
		}
		count++
		name := e.Name
		if e.AlsoCalled != "" {
			name = e.AlsoCalled
		}
		size := e.packed
		if e.WrongSize > 0 {
			size = e.WrongSize
		}

		head := make([]byte, 46)
		binary.LittleEndian.PutUint32(head, sigCentral)
		madeBy := uint16(20)
		if e.Mode != 0 {
			madeBy |= 3 << 8 // unix
		}
		binary.LittleEndian.PutUint16(head[4:], madeBy)
		binary.LittleEndian.PutUint16(head[6:], 20)
		binary.LittleEndian.PutUint32(head[12:], dosOf(time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)))
		binary.LittleEndian.PutUint32(head[16:], e.crc)
		binary.LittleEndian.PutUint32(head[20:], uint32(e.packed))
		binary.LittleEndian.PutUint32(head[24:], uint32(size))
		binary.LittleEndian.PutUint16(head[28:], uint16(len(name)))
		binary.LittleEndian.PutUint16(head[32:], uint16(len(e.Comment)))
		binary.LittleEndian.PutUint32(head[38:], e.Mode<<16)
		binary.LittleEndian.PutUint32(head[42:], uint32(e.at))
		out.Write(head)
		out.WriteString(name)
		out.WriteString(e.Comment)
	}
	dirSize := out.Len() - dirAt

	end := make([]byte, 22)
	binary.LittleEndian.PutUint32(end, sigEOCD)
	binary.LittleEndian.PutUint16(end[8:], uint16(count))
	binary.LittleEndian.PutUint16(end[10:], uint16(count))
	binary.LittleEndian.PutUint32(end[12:], uint32(dirSize))
	binary.LittleEndian.PutUint32(end[16:], uint32(dirAt))
	binary.LittleEndian.PutUint16(end[20:], uint16(len(comment)))
	out.Write(end)
	out.WriteString(comment)
	return out.Bytes()
}

func dosOf(at time.Time) uint32 {
	d := uint32(at.Year()-1980)<<9 | uint32(at.Month())<<5 | uint32(at.Day())
	t := uint32(at.Hour())<<11 | uint32(at.Minute())<<5 | uint32(at.Second()/2)
	return d<<16 | t
}

// onDisk writes bytes to a file and reads them back the way the program
// would, which is the only way to test a reader that works in offsets.
func onDisk(t *testing.T, name string, body []byte) *Tally {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := look(path)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return got
}

func readOf(t *testing.T, got *Tally, all bool) string {
	t.Helper()
	var out bytes.Buffer
	got.writeUp(all).render(&out, got.Name)
	return out.String()
}

// deflated is for the one case that needs real compression: a symlink
// whose target has to be inflated to be read.
func deflated(body string) []byte {
	var out bytes.Buffer
	w, _ := flate.NewWriter(&out, flate.BestCompression)
	w.Write([]byte(body))
	w.Close()
	return out.Bytes()
}
