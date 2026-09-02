package main

// An LZMA decoder, which is here for one reason: a 7z keeps its list of
// contents compressed.
//
// Every other archive this reads will tell you what is in it without
// decompressing anything. A zip has its index in the clear at the end, a
// tar announces each file in a plain block in front of it. A 7z puts the
// whole list of names, sizes and attributes into a stream and compresses
// that stream with LZMA, which means there is no way to answer "what is in
// this" without decoding LZMA. Not the files. Just the list.
//
// So this is a decoder, not a compressor, and it decodes exactly as much as
// the header needs. It is a range coder with a set of adaptive probability
// models, and the shape of it is fixed by the format rather than chosen: the
// numbers below are the ones in the specification and there is nothing to be
// gained by naming them differently.
//
// It is checked against archives written by 7-Zip itself, because a decoder
// that is subtly wrong does not fail. It produces plausible bytes, and
// plausible bytes here means a list of files that reads like a list of files
// and is not the one in the archive.

import (
	"errors"
	"fmt"
)

const (
	probBits  = 11
	probInit  = 1 << probBits >> 1
	moveBits  = 5
	topValue  = 1 << 24
	numStates = 12

	posBitsMax     = 4
	lenToPosStates = 4
	alignBits      = 4
	startPosModel  = 4
	endPosModel    = 14
	fullDistances  = 1 << (endPosModel >> 1)

	matchMinLen = 2
)

var errLZMA = errors.New("the compressed listing will not decode")

// rangeDecoder is the arithmetic coder the whole format is built on. It
// carries a window into the input and a running interval, and every
// decision below narrows the interval rather than reading bits.
type rangeDecoder struct {
	in    []byte
	at    int
	rng   uint32
	code  uint32
	broke bool
}

func newRangeDecoder(in []byte) (*rangeDecoder, error) {
	if len(in) < 5 {
		return nil, errLZMA
	}
	// The first byte is always zero and is there so the encoder can carry.
	r := &rangeDecoder{in: in, at: 1, rng: 0xffffffff}
	for range 4 {
		r.code = r.code<<8 | uint32(r.next())
	}
	return r, nil
}

func (r *rangeDecoder) next() byte {
	if r.at >= len(r.in) {
		r.broke = true
		return 0
	}
	b := r.in[r.at]
	r.at++
	return b
}

func (r *rangeDecoder) normalize() {
	if r.rng < topValue {
		r.rng <<= 8
		r.code = r.code<<8 | uint32(r.next())
	}
}

func (r *rangeDecoder) bit(prob *uint16) uint32 {
	bound := (r.rng >> probBits) * uint32(*prob)
	var bit uint32
	if r.code < bound {
		*prob += (1<<probBits - *prob) >> moveBits
		r.rng = bound
	} else {
		*prob -= *prob >> moveBits
		r.code -= bound
		r.rng -= bound
		bit = 1
	}
	r.normalize()
	return bit
}

// direct decodes bits with no model behind them, used for the high part of
// a long distance where the values are near enough uniform.
func (r *rangeDecoder) direct(count int) uint32 {
	var out uint32
	for range count {
		r.rng >>= 1
		r.code -= r.rng
		t := 0 - (r.code >> 31)
		r.code += r.rng & t
		out = out<<1 + t + 1
		r.normalize()
	}
	return out
}

func (r *rangeDecoder) tree(probs []uint16, bits int) uint32 {
	m := uint32(1)
	for range bits {
		m = m<<1 + r.bit(&probs[m])
	}
	return m - 1<<bits
}

// treeReverse is the same walk with the bits taken in the other order,
// which is how distances and the alignment are written.
func (r *rangeDecoder) treeReverse(probs []uint16, bits int) uint32 {
	m := uint32(1)
	var out uint32
	for i := range bits {
		b := r.bit(&probs[m])
		m = m<<1 + b
		out |= b << i
	}
	return out
}

func filledProbs(n int) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = probInit
	}
	return out
}

// lengths is the coder for how many bytes a match copies. Short lengths get
// their own small trees per position, because they are much the commonest.
type lengths struct {
	choice, choice2 uint16
	low, mid        [1 << posBitsMax][]uint16
	high            []uint16
}

func newLengths() *lengths {
	l := &lengths{choice: probInit, choice2: probInit, high: filledProbs(1 << 8)}
	for i := range l.low {
		l.low[i] = filledProbs(1 << 3)
		l.mid[i] = filledProbs(1 << 3)
	}
	return l
}

func (l *lengths) decode(r *rangeDecoder, posState uint32) uint32 {
	if r.bit(&l.choice) == 0 {
		return r.tree(l.low[posState], 3)
	}
	if r.bit(&l.choice2) == 0 {
		return 8 + r.tree(l.mid[posState], 3)
	}
	return 16 + r.tree(l.high, 8)
}

// lzma is one decoder with its models. The unpacked length is known from
// outside in a 7z, so there is no end marker to look for and the loop stops
// when it has produced what it was told to.
type lzma struct {
	lc, lp, pb uint32

	literal   []uint16
	isMatch   []uint16
	isRep     []uint16
	isRepG0   []uint16
	isRepG1   []uint16
	isRepG2   []uint16
	isRep0Len []uint16
	posSlot   [lenToPosStates][]uint16
	posSpec   []uint16
	align     []uint16

	lenMatch *lengths
	lenRep   *lengths

	state                  uint32
	rep0, rep1, rep2, rep3 uint32
	out                    []byte
}

// unpackLZMA decodes a stream given the five property bytes 7z carries
// beside it and the length it should come to.
func unpackLZMA(props, in []byte, want int) ([]byte, error) {
	if len(props) < 1 {
		return nil, errLZMA
	}
	d := props[0]
	if d >= 9*5*5 {
		return nil, fmt.Errorf("%w: the properties byte is %d", errLZMA, d)
	}
	l := &lzma{
		lc: uint32(d % 9),
		lp: uint32(d / 9 % 5),
		pb: uint32(d / 9 / 5),
	}
	if want < 0 || want > 1<<28 {
		return nil, fmt.Errorf("%w: it says it comes to %d bytes", errLZMA, want)
	}
	l.start(want)

	r, err := newRangeDecoder(in)
	if err != nil {
		return nil, err
	}
	if err := l.run(r, want); err != nil {
		return nil, err
	}
	return l.out, nil
}

func (l *lzma) start(want int) {
	l.literal = filledProbs(0x300 << (l.lc + l.lp))
	l.isMatch = filledProbs(numStates << posBitsMax)
	l.isRep = filledProbs(numStates)
	l.isRepG0 = filledProbs(numStates)
	l.isRepG1 = filledProbs(numStates)
	l.isRepG2 = filledProbs(numStates)
	l.isRep0Len = filledProbs(numStates << posBitsMax)
	for i := range l.posSlot {
		l.posSlot[i] = filledProbs(1 << 6)
	}
	l.posSpec = filledProbs(1 + fullDistances - endPosModel)
	l.align = filledProbs(1 << alignBits)
	l.lenMatch = newLengths()
	l.lenRep = newLengths()
	l.out = make([]byte, 0, want)
}

func (l *lzma) run(r *rangeDecoder, want int) error {
	posMask := uint32(1)<<l.pb - 1
	litMask := uint32(1)<<l.lp - 1

	for len(l.out) < want {
		if r.broke {
			return fmt.Errorf("%w: it ends part way through", errLZMA)
		}
		posState := uint32(len(l.out)) & posMask

		if r.bit(&l.isMatch[l.state<<posBitsMax+posState]) == 0 {
			l.literalAt(r, litMask)
			continue
		}

		var length uint32
		if r.bit(&l.isRep[l.state]) != 0 {
			// A repeat of one of the last four distances, which is what
			// most of a compressed stream turns out to be.
			if r.bit(&l.isRepG0[l.state]) == 0 {
				if r.bit(&l.isRep0Len[l.state<<posBitsMax+posState]) == 0 {
					l.state = shortRepState(l.state)
					if err := l.copyOne(); err != nil {
						return err
					}
					continue
				}
			} else {
				var dist uint32
				if r.bit(&l.isRepG1[l.state]) == 0 {
					dist = l.rep1
				} else {
					if r.bit(&l.isRepG2[l.state]) == 0 {
						dist = l.rep2
					} else {
						dist = l.rep3
						l.rep3 = l.rep2
					}
					l.rep2 = l.rep1
				}
				l.rep1 = l.rep0
				l.rep0 = dist
			}
			length = l.lenRep.decode(r, posState) + matchMinLen
			l.state = repState(l.state)
		} else {
			l.rep3, l.rep2, l.rep1 = l.rep2, l.rep1, l.rep0
			length = l.lenMatch.decode(r, posState) + matchMinLen
			l.state = matchState(l.state)
			dist, done := l.distance(r, length)
			if done {
				return nil
			}
			l.rep0 = dist
		}

		if err := l.copy(length); err != nil {
			return err
		}
	}
	return nil
}

func (l *lzma) literalAt(r *rangeDecoder, litMask uint32) {
	var prev byte
	if len(l.out) > 0 {
		prev = l.out[len(l.out)-1]
	}
	index := (uint32(len(l.out))&litMask)<<l.lc + uint32(prev)>>(8-l.lc)
	probs := l.literal[0x300*index:]

	sym := uint32(1)
	if l.state >= 7 {
		// After a match the byte at the same distance is a good guess, so
		// the model is told what it was and only has to code the
		// difference.
		var matched byte
		if at := len(l.out) - int(l.rep0) - 1; at >= 0 && at < len(l.out) {
			matched = l.out[at]
		}
		m := uint32(matched)
		offs := uint32(0x100)
		for sym < 0x100 {
			m <<= 1
			bit := m & offs
			got := r.bit(&probs[offs+bit+sym])
			sym = sym<<1 | got
			offs &= ^(bit ^ got<<8)
		}
	} else {
		for sym < 0x100 {
			sym = sym<<1 | r.bit(&probs[sym])
		}
	}
	l.out = append(l.out, byte(sym))
	l.state = literalState(l.state)
}

// distance decodes how far back a match reaches. The top of the value gets
// a model, the middle is uniform, and the bottom four bits get a model of
// their own because they carry the alignment of the data.
func (l *lzma) distance(r *rangeDecoder, length uint32) (uint32, bool) {
	slotIndex := length - matchMinLen
	if slotIndex >= lenToPosStates {
		slotIndex = lenToPosStates - 1
	}
	slot := r.tree(l.posSlot[slotIndex], 6)
	if slot < startPosModel {
		return slot, false
	}

	direct := int(slot>>1) - 1
	dist := (2 | slot&1) << direct
	if slot < endPosModel {
		dist += l.reverseAt(r, dist-slot, uint32(direct))
	} else {
		dist += r.direct(direct-alignBits) << alignBits
		dist += r.treeReverse(l.align, alignBits)
	}
	if dist == 0xffffffff {
		// The end marker, which a 7z header does not use and a stream from
		// somewhere else might.
		return 0, true
	}
	return dist, false
}

// reverseAt walks the shared distance models, which are one array indexed
// from part way in rather than one array per slot.
func (l *lzma) reverseAt(r *rangeDecoder, base, bits uint32) uint32 {
	m := uint32(1)
	var out uint32
	for i := range int(bits) {
		at := base + m
		if int(at) >= len(l.posSpec) {
			return out
		}
		b := r.bit(&l.posSpec[at])
		m = m<<1 + b
		out |= b << i
	}
	return out
}

func (l *lzma) copyOne() error {
	at := len(l.out) - int(l.rep0) - 1
	if at < 0 {
		return fmt.Errorf("%w: it reaches back before the start", errLZMA)
	}
	l.out = append(l.out, l.out[at])
	return nil
}

func (l *lzma) copy(length uint32) error {
	at := len(l.out) - int(l.rep0) - 1
	if at < 0 {
		return fmt.Errorf("%w: it reaches back before the start", errLZMA)
	}
	for range int(length) {
		l.out = append(l.out, l.out[at])
		at++
	}
	return nil
}

// The state machine, which is only used to pick which set of probabilities
// the next decision comes out of. The numbers are the ones in the
// specification.
func literalState(s uint32) uint32 {
	switch {
	case s < 4:
		return 0
	case s < 10:
		return s - 3
	}
	return s - 6
}

func matchState(s uint32) uint32 {
	if s < 7 {
		return 7
	}
	return 10
}

func repState(s uint32) uint32 {
	if s < 7 {
		return 8
	}
	return 11
}

func shortRepState(s uint32) uint32 {
	if s < 7 {
		return 9
	}
	return 11
}
