package main

// Reading a zip twice, on purpose.
//
// A zip file lists its contents in two places. At the end there is a
// central directory, which is the index: one record per file, with the
// name, the sizes, the checksum and the offset of where that file starts.
// At each of those offsets there is a local header, which says the same
// things again.
//
// Nothing in the format makes the two agree. They are written by the same
// program at the same moment, so in an honest archive they always do, and
// the specification never says they must.
//
// That gap is worth knowing about because different programs read
// different halves. A tool that lists an archive usually reads the central
// directory, because it is at the end and it is an index. A tool that
// streams one usually reads the local headers, because it is going
// forwards and they come first. So an archive can be built to show one set
// of files to whatever looked at it and hand a different set to whatever
// unpacked it, and both programs are behaving correctly.
//
// So this reads both and says when they disagree, which is the one thing
// no other tool pointed at an archive will tell you. Everything else here
// is ordinary careful reading of a format that is older than most of the
// people using it.

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	sigLocal   = 0x04034b50
	sigCentral = 0x02014b50
	sigEOCD    = 0x06054b50
	sigEOCD64  = 0x06064b50
	sigLoc64   = 0x07064b50
	sigData    = 0x08074b50
)

// Entry is one file as one of the two lists describes it.
type Entry struct {
	Name string
	// Size and Packed are the uncompressed and compressed lengths.
	Size   uint64
	Packed uint64
	CRC    uint32
	Method uint16
	Flags  uint16
	When   time.Time
	// Mode is the unix mode, when the archive was made somewhere that has
	// them. Zero when it was not.
	Mode uint32
	// At is where the local header for this entry starts.
	At uint64
	// MadeOn is the host system field, which says what made the archive.
	MadeOn uint16
	// MadeBy is the version, which for a unix zip is the tool's own idea
	// of what it supports.
	MadeBy uint16
	Extra  []byte
	// Comment is the per file comment, which almost nothing shows.
	Comment string
}

func (e Entry) Encrypted() bool { return e.Flags&1 != 0 }

func (e Entry) Symlink() bool {
	return e.MadeOn == 3 && e.Mode&0xf000 == 0xa000
}

func (e Entry) Directory() bool {
	return strings.HasSuffix(e.Name, "/") || e.Mode&0xf000 == 0x4000
}

func (e Entry) Executable() bool { return e.Mode&0o111 != 0 }
func (e Entry) SetUID() bool     { return e.Mode&0o4000 != 0 }
func (e Entry) SetGID() bool     { return e.Mode&0o2000 != 0 }

// Zip is what the two lists said, kept apart.
type Zip struct {
	Central []Entry
	Local   []Entry
	// Before is how many bytes sit in front of the first local header.
	Before uint64
	// Comment is the archive comment, which lives after the end of the
	// central directory and is the oldest hiding place in the format.
	Comment string
	Zip64   bool
	Gaps    []string
}

var errNotZip = errors.New("this does not have the end of a zip file in it")

// ReadZip reads the whole of both lists.
func ReadZip(file *os.File, size int64) (*Zip, error) {
	z := &Zip{}
	at, tail, err := findEOCD(file, size)
	if err != nil {
		return nil, err
	}

	count := int(binary.LittleEndian.Uint16(tail[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(tail[12:]))
	dirAt := uint64(binary.LittleEndian.Uint32(tail[16:]))
	commentLen := int(binary.LittleEndian.Uint16(tail[20:]))
	if commentLen > 0 && 22+commentLen <= len(tail) {
		z.Comment = string(tail[22 : 22+commentLen])
	}

	// The four kinds of "look in the other record" that zip64 uses. When
	// any of the fields is all ones the real value is in the zip64 record,
	// and an archive over four gigabytes or with more than 65535 files in
	// it is unreadable without this.
	if count == 0xffff || dirSize == 0xffffffff || dirAt == 0xffffffff {
		if n, s, a, ok := readZip64(file, at); ok {
			count, dirSize, dirAt = n, s, a
			z.Zip64 = true
		} else {
			z.Gaps = append(z.Gaps, "the zip64 record is missing or will not read, so the count of files may be short")
		}
	}

	dir := make([]byte, dirSize)
	if _, err := file.ReadAt(dir, int64(dirAt)); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the central directory will not read: %w", err)
	}
	z.Central = readCentral(dir, count, z)

	lowest := ^uint64(0)
	for _, e := range z.Central {
		if e.At < lowest {
			lowest = e.At
		}
	}
	if lowest != ^uint64(0) {
		z.Before = lowest
	}

	z.Local = z.readLocals(file, size)
	return z, nil
}

// findEOCD walks back from the end looking for the signature. It has to be
// a search rather than a seek because the archive comment sits after it and
// can be any length, which is also why a zip can have another whole file
// stuck on the end of it without complaint.
func findEOCD(file *os.File, size int64) (uint64, []byte, error) {
	const most = 66 * 1024
	from := size - most
	if from < 0 {
		from = 0
	}
	buf := make([]byte, size-from)
	if _, err := file.ReadAt(buf, from); err != nil && !errors.Is(err, io.EOF) {
		return 0, nil, err
	}
	for at := len(buf) - 22; at >= 0; at-- {
		if binary.LittleEndian.Uint32(buf[at:]) == sigEOCD {
			return uint64(from) + uint64(at), buf[at:], nil
		}
	}
	return 0, nil, errNotZip
}

func readZip64(file *os.File, eocdAt uint64) (count int, dirSize, dirAt uint64, ok bool) {
	if eocdAt < 20 {
		return 0, 0, 0, false
	}
	locator := make([]byte, 20)
	if _, err := file.ReadAt(locator, int64(eocdAt-20)); err != nil {
		return 0, 0, 0, false
	}
	if binary.LittleEndian.Uint32(locator) != sigLoc64 {
		return 0, 0, 0, false
	}
	recordAt := binary.LittleEndian.Uint64(locator[8:])

	record := make([]byte, 56)
	if _, err := file.ReadAt(record, int64(recordAt)); err != nil {
		return 0, 0, 0, false
	}
	if binary.LittleEndian.Uint32(record) != sigEOCD64 {
		return 0, 0, 0, false
	}
	return int(binary.LittleEndian.Uint64(record[32:])),
		binary.LittleEndian.Uint64(record[40:]),
		binary.LittleEndian.Uint64(record[48:]), true
}

func readCentral(dir []byte, count int, z *Zip) []Entry {
	var out []Entry
	at := 0
	for len(out) < count || count == 0 {
		if at+46 > len(dir) || binary.LittleEndian.Uint32(dir[at:]) != sigCentral {
			break
		}
		e := Entry{
			MadeBy: binary.LittleEndian.Uint16(dir[at+4:]),
			MadeOn: binary.LittleEndian.Uint16(dir[at+4:]) >> 8,
			Flags:  binary.LittleEndian.Uint16(dir[at+8:]),
			Method: binary.LittleEndian.Uint16(dir[at+10:]),
			CRC:    binary.LittleEndian.Uint32(dir[at+16:]),
			Packed: uint64(binary.LittleEndian.Uint32(dir[at+20:])),
			Size:   uint64(binary.LittleEndian.Uint32(dir[at+24:])),
			At:     uint64(binary.LittleEndian.Uint32(dir[at+42:])),
		}
		e.When = dosTime(binary.LittleEndian.Uint32(dir[at+12:]))
		external := binary.LittleEndian.Uint32(dir[at+38:])
		if e.MadeOn == 3 {
			e.Mode = external >> 16
		}

		nameLen := int(binary.LittleEndian.Uint16(dir[at+28:]))
		extraLen := int(binary.LittleEndian.Uint16(dir[at+30:]))
		commentLen := int(binary.LittleEndian.Uint16(dir[at+32:]))
		at += 46
		if at+nameLen+extraLen+commentLen > len(dir) {
			z.Gaps = append(z.Gaps, "the central directory ends in the middle of a record")
			break
		}
		e.Name = string(dir[at : at+nameLen])
		e.Extra = dir[at+nameLen : at+nameLen+extraLen]
		e.Comment = string(dir[at+nameLen+extraLen : at+nameLen+extraLen+commentLen])
		at += nameLen + extraLen + commentLen

		readZip64Extra(&e)
		out = append(out, e)
	}
	if count > 0 && len(out) != count {
		z.Gaps = append(z.Gaps, fmt.Sprintf(
			"the end record says %d files and the central directory has %d in it",
			count, len(out)))
	}
	return out
}

// readZip64Extra fills in the fields that were held back as all ones.
func readZip64Extra(e *Entry) {
	extra := e.Extra
	for len(extra) >= 4 {
		tag := binary.LittleEndian.Uint16(extra)
		size := int(binary.LittleEndian.Uint16(extra[2:]))
		if 4+size > len(extra) {
			return
		}
		body := extra[4 : 4+size]
		if tag == 0x0001 {
			at := 0
			take := func() uint64 {
				if at+8 > len(body) {
					return 0
				}
				v := binary.LittleEndian.Uint64(body[at:])
				at += 8
				return v
			}
			if e.Size == 0xffffffff {
				e.Size = take()
			}
			if e.Packed == 0xffffffff {
				e.Packed = take()
			}
			if e.At == 0xffffffff {
				e.At = take()
			}
		}
		extra = extra[4+size:]
	}
}

// readLocals walks forwards through the file reading the header in front
// of each stored file, which is the other list.
func (z *Zip) readLocals(file *os.File, size int64) []Entry {
	var out []Entry
	at := int64(z.Before)
	head := make([]byte, 30)

	for at+30 <= size {
		if _, err := file.ReadAt(head, at); err != nil {
			break
		}
		if binary.LittleEndian.Uint32(head) != sigLocal {
			break
		}
		e := Entry{
			Flags:  binary.LittleEndian.Uint16(head[6:]),
			Method: binary.LittleEndian.Uint16(head[8:]),
			CRC:    binary.LittleEndian.Uint32(head[14:]),
			Packed: uint64(binary.LittleEndian.Uint32(head[18:])),
			Size:   uint64(binary.LittleEndian.Uint32(head[22:])),
			At:     uint64(at),
		}
		e.When = dosTime(binary.LittleEndian.Uint32(head[10:]))
		nameLen := int(binary.LittleEndian.Uint16(head[26:]))
		extraLen := int(binary.LittleEndian.Uint16(head[28:]))

		rest := make([]byte, nameLen+extraLen)
		if _, err := file.ReadAt(rest, at+30); err != nil {
			break
		}
		e.Name = string(rest[:nameLen])
		e.Extra = rest[nameLen:]
		readZip64Extra(&e)
		out = append(out, e)

		// The sizes can be zero here and written after the data instead,
		// which is what the third flag bit means. When that happens the
		// only way forwards is the size the central directory gave, and
		// if the two disagree this is where it shows.
		packed := e.Packed
		if e.Flags&8 != 0 || packed == 0 {
			if fromDir, ok := z.packedFromDir(e.Name, uint64(at)); ok {
				packed = fromDir
			}
		}
		next := at + 30 + int64(nameLen) + int64(extraLen) + int64(packed)
		if e.Flags&8 != 0 {
			next += descriptorLength(file, next, z.Zip64)
		}
		if next <= at {
			z.Gaps = append(z.Gaps, "an entry says it is nothing, so reading forwards stopped")
			break
		}
		at = next

		if len(out) > 200000 {
			z.Gaps = append(z.Gaps, "there are more entries than this will walk")
			break
		}
	}
	return out
}

func (z *Zip) packedFromDir(name string, at uint64) (uint64, bool) {
	for _, e := range z.Central {
		if e.At == at || e.Name == name {
			return e.Packed, true
		}
	}
	return 0, false
}

// descriptorLength is the length of the little record that follows the data
// when the sizes were not known in front of it.
func descriptorLength(file *os.File, at int64, zip64 bool) int64 {
	head := make([]byte, 4)
	if _, err := file.ReadAt(head, at); err != nil {
		return 0
	}
	size := int64(12)
	if zip64 {
		size = 20
	}
	if binary.LittleEndian.Uint32(head) == sigData {
		return size + 4
	}
	return size
}

// dosTime is the format the zip carries: a date and a time packed into two
// sixteen bit words, with two second resolution and no timezone at all.
func dosTime(packed uint32) time.Time {
	t := packed & 0xffff
	d := packed >> 16
	if d == 0 {
		return time.Time{}
	}
	return time.Date(
		int(d>>9)+1980, time.Month(d>>5&0xf), int(d&0x1f),
		int(t>>11), int(t>>5&0x3f), int(t&0x1f)*2, 0, time.UTC)
}

// looksLikeZip is the check for the front of the file rather than the end,
// used to tell what was handed over before deciding how to read it.
func looksLikeZip(head []byte) bool {
	return len(head) >= 4 && binary.LittleEndian.Uint32(head) == sigLocal
}

// madeOnName is the host system field, which is the closest a zip comes to
// saying what made it.
func madeOnName(n uint16) string {
	switch n {
	case 0:
		return "MS-DOS or Windows"
	case 3:
		return "Unix"
	case 7:
		return "a Mac"
	case 10:
		return "Windows NTFS"
	case 19:
		return "macOS"
	}
	return fmt.Sprintf("system %d", n)
}

func methodName(m uint16) string {
	switch m {
	case 0:
		return "stored, not compressed"
	case 8:
		return "deflate"
	case 9:
		return "deflate64"
	case 12:
		return "bzip2"
	case 14:
		return "lzma"
	case 93:
		return "zstd"
	case 95:
		return "xz"
	case 99:
		return "AES encrypted"
	}
	return fmt.Sprintf("method %d", m)
}

// unixExtra pulls the owner out of the extra field, because a zip made on
// unix can carry the numeric user and group of whoever made it and almost
// nobody knows it is in there.
func unixExtra(extra []byte) (uid, gid int, found bool) {
	for len(extra) >= 4 {
		tag := binary.LittleEndian.Uint16(extra)
		size := int(binary.LittleEndian.Uint16(extra[2:]))
		if 4+size > len(extra) {
			return 0, 0, false
		}
		body := extra[4 : 4+size]
		switch tag {
		case 0x7875: // Info-ZIP new unix
			if len(body) >= 3 {
				at := 1
				uidLen := int(body[at])
				at++
				if at+uidLen <= len(body) {
					uid = int(littleNumber(body[at : at+uidLen]))
					at += uidLen
				}
				if at < len(body) {
					gidLen := int(body[at])
					at++
					if at+gidLen <= len(body) {
						gid = int(littleNumber(body[at : at+gidLen]))
					}
				}
				return uid, gid, true
			}
		case 0x7855: // Info-ZIP unix, no fields
		case 0x5855: // old Info-ZIP unix
			if len(body) >= 12 {
				return int(binary.LittleEndian.Uint16(body[8:])),
					int(binary.LittleEndian.Uint16(body[10:])), true
			}
		}
		extra = extra[4+size:]
	}
	return 0, 0, false
}

func littleNumber(b []byte) uint64 {
	var v uint64
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint64(b[i])
	}
	return v
}

// linkTarget reads the body of a symlink entry, which is the path it points
// at and is the whole question for one.
func linkTarget(file *os.File, e Entry) (string, bool) {
	if e.Size == 0 || e.Size > 4096 || e.Encrypted() {
		return "", false
	}
	head := make([]byte, 30)
	if _, err := file.ReadAt(head, int64(e.At)); err != nil {
		return "", false
	}
	if binary.LittleEndian.Uint32(head) != sigLocal {
		return "", false
	}
	nameLen := int(binary.LittleEndian.Uint16(head[26:]))
	extraLen := int(binary.LittleEndian.Uint16(head[28:]))
	from := int64(e.At) + 30 + int64(nameLen) + int64(extraLen)

	body := make([]byte, e.Packed)
	if _, err := file.ReadAt(body, from); err != nil {
		return "", false
	}
	switch e.Method {
	case 0:
		return string(body), true
	case 8:
		out, err := inflate(body, int(e.Size))
		if err != nil {
			return "", false
		}
		return string(out), true
	}
	return "", false
}

func inflate(body []byte, size int) ([]byte, error) {
	r := newFlateReader(bytes.NewReader(body))
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, int64(size)+1))
}

func newFlateReader(r io.Reader) io.ReadCloser { return flate.NewReader(r) }
