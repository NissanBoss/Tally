package main

// Reading a tar, which keeps its list in a different place and has a
// different set of things worth knowing about.
//
// A tar has one list, not two: each file is announced by a header block
// immediately in front of it and there is no index anywhere. So the whole
// question this program asks of a zip does not arise here. What does arise
// is everything a tar can carry that a zip cannot: a device node, a fifo,
// a hard link, and the numeric owner and the login name of whoever made
// it, which travel in every ordinary tar and which almost nobody knows are
// in there.

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Piece is one entry of a tar, in the shape the rest of the program uses.
type Piece struct {
	Name string
	Size int64
	Mode int64
	// Kind is what the typeflag said it was.
	Kind string
	// Link is where a symlink or a hard link points.
	Link  string
	UID   int
	GID   int
	User  string
	Group string
	When  time.Time
	// Major and Minor are set for a device node.
	Major, Minor int64
}

type Tar struct {
	Pieces []Piece
	// Packed and Unpacked are the size on disk and the size it comes to.
	Packed   int64
	Unpacked int64
	// Wrapper is what the tar was compressed with, or empty.
	Wrapper string
	// Wrapped is the name the gzip header says the file had before it was
	// compressed.
	Wrapped string
	Gaps    []string
}

var errNotTar = errors.New("this does not read as a tar")

// ReadTar unwraps whatever the tar was compressed with and walks it.
func ReadTar(file *os.File, size int64, wrapper string) (*Tar, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	t := &Tar{Packed: size, Wrapper: wrapper}

	var stream io.Reader = file
	switch wrapper {
	case "gzip":
		gz, err := gzip.NewReader(file)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		// The gzip header carries the name the file had before it was
		// compressed, which keeps turning out to be somebody's own path.
		t.Wrapped = gz.Name
		stream = gz
	case "bzip2":
		stream = bzip2.NewReader(file)
	}

	// A tar can say it is enormous and be a few kilobytes, so the amount
	// read is capped and the report says when the cap was reached rather
	// than sitting there unpacking a bomb into memory.
	const mostEntries = 200000
	r := tar.NewReader(io.LimitReader(stream, 8<<30))

	for {
		head, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if len(t.Pieces) == 0 {
				return nil, errNotTar
			}
			t.Gaps = append(t.Gaps, "the tar stops part way through: "+err.Error())
			break
		}
		p := Piece{
			Name:  head.Name,
			Size:  head.Size,
			Mode:  head.Mode,
			Kind:  kindOfPiece(head.Typeflag),
			Link:  head.Linkname,
			UID:   head.Uid,
			GID:   head.Gid,
			User:  head.Uname,
			Group: head.Gname,
			When:  head.ModTime,
			Major: head.Devmajor,
			Minor: head.Devminor,
		}
		t.Pieces = append(t.Pieces, p)
		t.Unpacked += head.Size
		if len(t.Pieces) > mostEntries {
			t.Gaps = append(t.Gaps, "there are more entries than this will walk")
			break
		}
	}
	if len(t.Pieces) == 0 {
		return nil, errNotTar
	}
	return t, nil
}

func kindOfPiece(flag byte) string {
	switch flag {
	case tar.TypeReg:
		return "file"
	case tar.TypeLink:
		return "hard link"
	case tar.TypeSymlink:
		return "symlink"
	case tar.TypeChar:
		return "character device"
	case tar.TypeBlock:
		return "block device"
	case tar.TypeDir:
		return "directory"
	case tar.TypeFifo:
		return "fifo"
	}
	return fmt.Sprintf("type %q", string(flag))
}

func (p Piece) Executable() bool { return p.Mode&0o111 != 0 }
func (p Piece) SetUID() bool     { return p.Mode&0o4000 != 0 }
func (p Piece) SetGID() bool     { return p.Mode&0o2000 != 0 }

func (p Piece) Device() bool {
	return p.Kind == "character device" || p.Kind == "block device" || p.Kind == "fifo"
}

// wrapperOf works out what a file was compressed with from the first few
// bytes, because the name is a claim and the bytes are the fact.
func wrapperOf(head []byte) string {
	switch {
	case len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b:
		return "gzip"
	case len(head) >= 3 && string(head[:3]) == "BZh":
		return "bzip2"
	case len(head) >= 6 && string(head[:6]) == "\xfd7zXZ\x00":
		return "xz"
	case len(head) >= 4 && head[0] == 0x28 && head[1] == 0xb5 &&
		head[2] == 0x2f && head[3] == 0xfd:
		return "zstd"
	case len(head) >= 4 && string(head[:4]) == "\x04\x22\x4d\x18":
		return "lz4"
	case len(head) >= 6 && string(head[:6]) == "7z\xbc\xaf\x27\x1c":
		return "7z"
	case len(head) >= 6 && string(head[:6]) == "Rar!\x1a\x07":
		return "rar"
	}
	return ""
}

// looksLikeTar checks the magic in the middle of the first header block,
// which is where tar keeps it.
func looksLikeTar(head []byte) bool {
	if len(head) < 265 {
		return false
	}
	magic := string(head[257:262])
	return magic == "ustar" || strings.HasPrefix(magic, "ustar")
}
