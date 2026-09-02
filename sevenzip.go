package main

// Reading a 7z, where the list of contents is the thing that is hidden.
//
// The other formats here answer "what is in this" out of plain bytes. A 7z
// does not. It has a thirty two byte signature that points at a header, and
// that header is almost always an encoded header: a small description of how
// to decompress the real header, which is an LZMA stream. So the names, the
// sizes and the attributes all live behind a decoder, and that is why
// lzma.go exists.
//
// It also has two things no other format here has, and both of them belong
// in a report about what an archive will do:
//
// An archive can be built so the file names themselves are encrypted, not
// just the file contents. Opened without the password it will not say what
// it holds at all. That is not the same as being empty and nothing tells
// people which one they are looking at.
//
// And it can carry anti-items: entries whose job is to delete a file when
// the archive is unpacked, meant for incremental backups. An entry that
// takes something off your disk rather than putting something on it.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf16"
)

const (
	idEnd            = 0x00
	idHeader         = 0x01
	idMainStreams    = 0x04
	idFilesInfo      = 0x05
	idPackInfo       = 0x06
	idUnpackInfo     = 0x07
	idSubStreamsInfo = 0x08
	idSize           = 0x09
	idCRC            = 0x0a
	idFolder         = 0x0b
	idCodersSize     = 0x0c
	idNumUnpack      = 0x0d
	idEmptyStream    = 0x0e
	idEmptyFile      = 0x0f
	idAnti           = 0x10
	idName           = 0x11
	idMTime          = 0x14
	idAttributes     = 0x15
	idEncodedHeader  = 0x17
	idDummy          = 0x19
)

var (
	errNot7z    = errors.New("this does not read as a 7z")
	errLocked7z = errors.New("the list of contents in this 7z is encrypted")
)

// Seven is what came out of a 7z.
type Seven struct {
	Files []SevenFile
	// Method is what the file data was compressed with, which is not
	// necessarily what the header was.
	Method string
	// HeaderPacked says the list of contents was itself compressed.
	HeaderPacked bool
	// Locked says the names are encrypted and none of them could be read.
	Locked bool
	// DataLocked says the contents are encrypted and the names were not.
	DataLocked bool
	// subSizes is the size of each stored file, worked out from how the
	// folders divide up.
	subSizes []uint64
	Gaps     []string
}

type SevenFile struct {
	Name string
	Size uint64
	// Attr is the Windows attribute word. When bit 15 is set, the high
	// half is a unix mode, which is how a 7z made on unix carries a
	// symlink or an executable bit.
	Attr  uint32
	When  time.Time
	IsDir bool
	// Anti marks an entry that deletes a file rather than writing one.
	Anti bool
}

func (f SevenFile) unixMode() uint32 {
	if f.Attr&0x8000 == 0 {
		return 0
	}
	return f.Attr >> 16
}

func (f SevenFile) Symlink() bool { return f.unixMode()&0xf000 == 0xa000 }

// ReadSeven walks the signature, finds the header, decodes it when it is
// compressed, and reads the list out of it.
func ReadSeven(file *os.File, size int64) (*Seven, error) {
	sig := make([]byte, 32)
	if _, err := file.ReadAt(sig, 0); err != nil {
		return nil, errNot7z
	}
	if string(sig[:6]) != "7z\xbc\xaf\x27\x1c" {
		return nil, errNot7z
	}

	at := int64(binary.LittleEndian.Uint64(sig[12:]))
	length := int64(binary.LittleEndian.Uint64(sig[20:]))
	if length <= 0 || at < 0 || 32+at+length > size {
		return nil, fmt.Errorf("%w: the header is not where the signature says", errNot7z)
	}

	head := make([]byte, length)
	if _, err := file.ReadAt(head, 32+at); err != nil {
		return nil, err
	}

	s := &Seven{}
	body := &bytes7{in: head}
	switch id := body.byte(); id {
	case idHeader:
		return s, s.readHeader(body)
	case idEncodedHeader:
		s.HeaderPacked = true
		real, err := s.unpackHeader(file, body)
		if err != nil {
			return s, err
		}
		inner := &bytes7{in: real}
		if inner.byte() != idHeader {
			return s, fmt.Errorf("%w: what the header decoded to is not a header", errNot7z)
		}
		return s, s.readHeader(inner)
	default:
		return nil, fmt.Errorf("%w: the header starts with %#x", errNot7z, id)
	}
}

// unpackHeader follows the little archive that holds the real header.
func (s *Seven) unpackHeader(file *os.File, in *bytes7) ([]byte, error) {
	packs, folders, err := s.streamsInfo(in)
	if err != nil {
		return nil, err
	}
	if len(folders) != 1 || len(packs) == 0 {
		return nil, fmt.Errorf("%w: the header is packed in a way this does not read", errNot7z)
	}
	folder := folders[0]

	for _, c := range folder.coders {
		if c.id == aesCoder {
			s.Locked = true
			return nil, errLocked7z
		}
	}
	if len(folder.coders) != 1 {
		return nil, fmt.Errorf("%w: the header goes through %d coders",
			errNot7z, len(folder.coders))
	}

	coder := folder.coders[0]
	packed := make([]byte, packs[0].size)
	if _, err := file.ReadAt(packed, 32+int64(packs[0].at)); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	switch coder.id {
	case copyCoder:
		return packed, nil
	case lzmaCoder:
		return unpackLZMA(coder.props, packed, int(folder.unpacked))
	}
	return nil, fmt.Errorf("%w: the header is compressed with %s",
		errNot7z, coderName(coder.id))
}

const (
	copyCoder  = 0x00
	lzmaCoder  = 0x030101
	lzma2Coder = 0x21
	bcjCoder   = 0x03030103
	deltaCoder = 0x03
	aesCoder   = 0x06f10701
	bzipCoder  = 0x040202
	ppmdCoder  = 0x030401
)

func coderName(id uint64) string {
	switch id {
	case copyCoder:
		return "no compression"
	case lzmaCoder:
		return "LZMA"
	case lzma2Coder:
		return "LZMA2"
	case bzipCoder:
		return "bzip2"
	case ppmdCoder:
		return "PPMd"
	case deltaCoder:
		return "a delta filter"
	case bcjCoder:
		return "an x86 filter"
	case aesCoder:
		return "AES-256"
	}
	return fmt.Sprintf("coder %#x", id)
}

type pack7 struct{ at, size uint64 }

type coder7 struct {
	id    uint64
	props []byte
	in    uint64
	out   uint64
}

type folder7 struct {
	coders   []coder7
	unpacked uint64
}

// streamsInfo reads the part that says where the compressed blocks are and
// how to turn them back into bytes.
func (s *Seven) streamsInfo(in *bytes7) ([]pack7, []folder7, error) {
	var packs []pack7
	var folders []folder7
	packBase := uint64(0)

	for {
		switch id := in.byte(); id {
		case idEnd:
			return packs, folders, in.err
		case idPackInfo:
			packBase = in.number()
			count := in.number()
			if count > 1<<20 {
				return nil, nil, errNot7z
			}
			for {
				next := in.byte()
				if next == idEnd {
					break
				}
				if next == idSize {
					at := packBase
					for range int(count) {
						size := in.number()
						packs = append(packs, pack7{at: at, size: size})
						at += size
					}
					continue
				}
				in.skipProperty()
			}
		case idUnpackInfo:
			var err error
			folders, err = s.folders(in)
			if err != nil {
				return nil, nil, err
			}
		case idSubStreamsInfo:
			s.subSizes = in.subStreams(folders)
		default:
			if in.err != nil {
				return nil, nil, in.err
			}
			in.skipProperty()
		}
		if in.err != nil {
			return nil, nil, in.err
		}
	}
}

func (s *Seven) folders(in *bytes7) ([]folder7, error) {
	if in.byte() != idFolder {
		return nil, fmt.Errorf("%w: no folder record", errNot7z)
	}
	count := in.number()
	if count > 1<<20 {
		return nil, errNot7z
	}
	if in.byte() != 0 {
		return nil, fmt.Errorf("%w: the folders are kept somewhere else", errNot7z)
	}

	out := make([]folder7, 0, count)
	for range int(count) {
		f := folder7{}
		coders := in.number()
		if coders > 32 {
			return nil, errNot7z
		}
		outputs := uint64(0)
		inputs := uint64(0)
		for range int(coders) {
			flags := in.byte()
			idLen := int(flags & 0x0f)
			var id uint64
			for range idLen {
				id = id<<8 | uint64(in.byte())
			}
			c := coder7{id: id, in: 1, out: 1}
			if flags&0x10 != 0 {
				c.in = in.number()
				c.out = in.number()
			}
			if flags&0x20 != 0 {
				n := in.number()
				c.props = in.take(int(n))
			}
			inputs += c.in
			outputs += c.out
			f.coders = append(f.coders, c)
		}
		// The bind pairs and the packed stream indexes describe how the
		// coders are wired together. Nothing here needs the wiring, only
		// how many numbers to step over.
		pairs := outputs - 1
		for range int(pairs) {
			in.number()
			in.number()
		}
		if inputs-pairs > 1 {
			for range int(inputs - pairs) {
				in.number()
			}
		}
		out = append(out, f)
		if in.err != nil {
			return nil, in.err
		}
	}

	if in.byte() != idCodersSize {
		return nil, fmt.Errorf("%w: no sizes for the folders", errNot7z)
	}
	for i := range out {
		for _, c := range out[i].coders {
			for range int(c.out) {
				out[i].unpacked = in.number()
			}
		}
	}
	for {
		switch in.byte() {
		case idEnd:
			return out, in.err
		case idCRC:
			in.digests(len(out))
		default:
			if in.err != nil {
				return nil, in.err
			}
			in.skipProperty()
		}
	}
}

// readHeader is the part that names the files.
func (s *Seven) readHeader(in *bytes7) error {
	var sizes []uint64

	for {
		switch id := in.byte(); id {
		case idEnd:
			return in.err
		case idMainStreams:
			packs, folders, err := s.streamsInfo(in)
			if err != nil {
				return err
			}
			_ = packs
			sizes = s.sizesFrom(folders)
		case idFilesInfo:
			return s.filesInfo(in, sizes)
		default:
			if in.err != nil {
				return in.err
			}
			in.skipProperty()
		}
	}
}

// sizesFrom gives the size of each stored file. When several files share a
// folder the substream record has already divided it up; when they do not,
// a folder is one file and its own size is the answer.
func (s *Seven) sizesFrom(folders []folder7) []uint64 {
	for _, f := range folders {
		for _, c := range f.coders {
			switch c.id {
			case aesCoder:
				s.DataLocked = true
			case copyCoder:
			default:
				if s.Method == "" {
					s.Method = coderName(c.id)
				}
			}
		}
	}
	if len(s.subSizes) > 0 {
		return s.subSizes
	}
	out := make([]uint64, 0, len(folders))
	for _, f := range folders {
		out = append(out, f.unpacked)
	}
	return out
}

func (s *Seven) filesInfo(in *bytes7, sizes []uint64) error {
	count := in.number()
	if count > 1<<22 {
		return errNot7z
	}
	files := make([]SevenFile, count)

	var emptyStream, emptyFile, anti []bool

	for {
		id := in.number()
		if id == idEnd || in.err != nil {
			break
		}
		length := int(in.number())
		body := &bytes7{in: in.take(length)}

		switch id {
		case idEmptyStream:
			emptyStream = body.bits(int(count))
		case idEmptyFile:
			emptyFile = body.bits(countTrue(emptyStream))
		case idAnti:
			anti = body.bits(countTrue(emptyStream))
		case idName:
			if body.byte() != 0 {
				s.Gaps = append(s.Gaps, "the names are kept in another stream and were not read")
				break
			}
			for i := range files {
				files[i].Name = body.name()
			}
		case idAttributes:
			defined := body.defined(int(count))
			if body.byte() != 0 {
				break
			}
			for i := range files {
				if defined[i] {
					files[i].Attr = uint32(body.u32())
				}
			}
		case idMTime:
			defined := body.defined(int(count))
			if body.byte() != 0 {
				break
			}
			for i := range files {
				if defined[i] {
					files[i].When = fromFiletime(body.u64())
				}
			}
		}
		if in.err != nil {
			return in.err
		}
	}

	// A file with no stream is a directory unless it was marked as an
	// empty file, and it is an anti-item if it was marked as one of those.
	at := 0
	empties := 0
	for i := range files {
		noStream := i < len(emptyStream) && emptyStream[i]
		if !noStream {
			if at < len(sizes) {
				files[i].Size = sizes[at]
			}
			at++
			continue
		}
		isFile := empties < len(emptyFile) && emptyFile[empties]
		files[i].IsDir = !isFile
		if empties < len(anti) && anti[empties] {
			files[i].Anti = true
			files[i].IsDir = false
		}
		empties++
	}

	s.Files = files
	return nil
}

func countTrue(b []bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

// fromFiletime turns Windows' hundred nanosecond count since 1601 into a
// time, which is what a 7z stores whatever machine wrote it.
func fromFiletime(n uint64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	const toUnix = 11644473600
	return time.Unix(int64(n/10000000)-toUnix, int64(n%10000000)*100).UTC()
}

// bytes7 is a cursor over a header with the format's own number encoding on
// it. Every read is bounds checked and sets err rather than panicking,
// because the input is a file somebody else wrote.
type bytes7 struct {
	in  []byte
	at  int
	err error
}

func (b *bytes7) byte() byte {
	if b.at >= len(b.in) {
		b.fail()
		return 0
	}
	v := b.in[b.at]
	b.at++
	return v
}

func (b *bytes7) fail() {
	if b.err == nil {
		b.err = fmt.Errorf("%w: it ends part way through a record", errNot7z)
	}
}

func (b *bytes7) take(n int) []byte {
	if n < 0 || b.at+n > len(b.in) {
		b.fail()
		return nil
	}
	out := b.in[b.at : b.at+n]
	b.at += n
	return out
}

// number is the format's variable length integer: the top bits of the
// first byte say how many more bytes follow, and the rest of that byte is
// the high part of the value.
func (b *bytes7) number() uint64 {
	first := b.byte()
	mask := byte(0x80)
	var value uint64
	for i := range 8 {
		if first&mask == 0 {
			high := uint64(first) & uint64(mask-1)
			return value | high<<(8*i)
		}
		value |= uint64(b.byte()) << (8 * i)
		mask >>= 1
		if b.err != nil {
			return value
		}
	}
	return value
}

func (b *bytes7) u32() uint32 {
	v := b.take(4)
	if len(v) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(v)
}

func (b *bytes7) u64() uint64 {
	v := b.take(8)
	if len(v) < 8 {
		return 0
	}
	return binary.LittleEndian.Uint64(v)
}

// bits reads a bit vector, most significant bit first.
func (b *bytes7) bits(n int) []bool {
	out := make([]bool, n)
	var cur byte
	mask := byte(0)
	for i := range n {
		if mask == 0 {
			cur = b.byte()
			mask = 0x80
		}
		out[i] = cur&mask != 0
		mask >>= 1
	}
	return out
}

// defined is a bit vector with a byte in front saying they are all set,
// which is the common case and saves the vector.
func (b *bytes7) defined(n int) []bool {
	if b.byte() != 0 {
		out := make([]bool, n)
		for i := range out {
			out[i] = true
		}
		return out
	}
	return b.bits(n)
}

// name reads one null terminated UTF-16 name.
func (b *bytes7) name() string {
	var units []uint16
	for {
		if b.at+2 > len(b.in) {
			b.fail()
			break
		}
		u := binary.LittleEndian.Uint16(b.in[b.at:])
		b.at += 2
		if u == 0 {
			break
		}
		units = append(units, u)
	}
	return string(utf16.Decode(units))
}

func (b *bytes7) skipProperty() {
	n := b.number()
	b.take(int(n))
}

func (b *bytes7) skipUntilEnd() {
	depth := 1
	for depth > 0 && b.err == nil {
		switch b.byte() {
		case idEnd:
			depth--
		default:
			b.skipProperty()
		}
	}
}

func looksLikeSeven(head []byte) bool {
	return len(head) >= 6 && string(head[:6]) == "7z\xbc\xaf\x27\x1c"
}

// digests is the checksum record, which turns up after several other
// records and is not length prefixed like the properties in FilesInfo are.
// Reading it as though it were is what made this stop half way through the
// first real archive it was pointed at.
func (b *bytes7) digests(count int) {
	defined := b.defined(count)
	for range countTrue(defined) {
		b.u32()
	}
}

// subStreams says how the files inside a folder divide up its output. A
// folder usually holds several files, and without this every file after
// the first in a folder has no size at all.
func (b *bytes7) subStreams(folders []folder7) []uint64 {
	counts := make([]uint64, len(folders))
	for i := range counts {
		counts[i] = 1
	}
	var sizes []uint64

	for {
		switch id := b.byte(); id {
		case idEnd:
			if len(sizes) == 0 {
				for _, f := range folders {
					sizes = append(sizes, f.unpacked)
				}
			}
			return sizes
		case idNumUnpack:
			for i := range counts {
				counts[i] = b.number()
			}
		case idSize:
			// All but the last size in each folder is written down; the
			// last one is whatever is left of the folder.
			for i, f := range folders {
				if counts[i] == 0 {
					continue
				}
				left := f.unpacked
				for range int(counts[i]) - 1 {
					size := b.number()
					sizes = append(sizes, size)
					left -= size
				}
				sizes = append(sizes, left)
			}
		case idCRC:
			unknown := 0
			for i, f := range folders {
				if counts[i] != 1 || f.unpacked == 0 {
					unknown += int(counts[i])
				}
			}
			b.digests(unknown)
		default:
			if b.err != nil {
				return sizes
			}
			b.skipProperty()
		}
		if b.err != nil {
			return sizes
		}
	}
}
