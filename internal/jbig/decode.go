package jbig

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// maxImageBytes bounds the decoded bitmap so a corrupt BIH cannot trigger a
// huge allocation.
const maxImageBytes = 1 << 30

// decoder is the QM arithmetic decoder of T.82 matching encoder. Bits 32..47
// of c hold the code value compared against a; the next byte enters at
// bits 24..31.
type decoder struct {
	data []byte // one stripe's coded data, still escaped (FF 00)
	pos  int
	c    uint64
	a    uint32
	ct   int
}

// byteIn returns the next data byte, or 0 past the end of the stripe
// (the encoder may drop trailing zero bytes).
func (d *decoder) byteIn() uint64 {
	if d.pos >= len(d.data) {
		return 0
	}
	b := d.data[d.pos]
	d.pos++
	if b == markerEsc {
		d.pos++ // skip the stuffing byte
	}
	return uint64(b)
}

func (d *decoder) init(data []byte) {
	d.data, d.pos = data, 0
	d.c = d.byteIn()<<40 | d.byteIn()<<32 | d.byteIn()<<24
	d.ct = 8
	d.a = 0x10000
}

func (d *decoder) decode(cx *context) uint8 {
	s := &qmTable[cx.st]
	lsz := s.lsz
	a := d.a - lsz
	var pix uint8
	if uint32(d.c>>32) < a {
		// Lower sub-interval: the MPS unless exchanged.
		if a >= 0x8000 {
			d.a = a
			return cx.mps
		}
		if a < lsz {
			pix = cx.mps ^ 1
			if s.swtch {
				cx.mps ^= 1
			}
			cx.st = s.nlps
		} else {
			pix = cx.mps
			cx.st = s.nmps
		}
		d.a = a
	} else {
		// Upper sub-interval (size lsz): the LPS unless exchanged.
		d.c -= uint64(a) << 32
		if a < lsz {
			pix = cx.mps
			cx.st = s.nmps
		} else {
			pix = cx.mps ^ 1
			if s.swtch {
				cx.mps ^= 1
			}
			cx.st = s.nlps
		}
		d.a = lsz
	}
	for d.a < 0x8000 {
		d.a <<= 1
		d.c <<= 1
		d.ct--
		if d.ct == 0 {
			d.c |= d.byteIn() << 24
			d.ct = 8
		}
	}
	return pix
}

// Decode a single-layer bi-level image produced by Encode or any T.85 encoder
// using the same subset: DL=0, D=0, P=1, MX=0, MY=0, options 0x40 (two-line
// template) or 0x00 (three-line template), SDNORM stripe ends, no NEWLEN.
// Rows are returned as (width+7)/8 bytes each, MSB first, 1 = ink.
func Decode(bie []byte) (width, height int, rows []byte, err error) {
	if len(bie) < bihLen {
		return 0, 0, nil, fmt.Errorf("jbig: stream too short for BIH (%d bytes)", len(bie))
	}
	dl, d, p := bie[0], bie[1], bie[2]
	xd := binary.BigEndian.Uint32(bie[4:])
	yd := binary.BigEndian.Uint32(bie[8:])
	l0 := binary.BigEndian.Uint32(bie[12:])
	mx, my, order, options := bie[16], bie[17], bie[18], bie[19]
	switch {
	case dl != 0 || d != 0:
		return 0, 0, nil, fmt.Errorf("jbig: resolution layers DL=%d D=%d not supported (need 0, 0)", dl, d)
	case p != 1:
		return 0, 0, nil, fmt.Errorf("jbig: %d bit planes not supported (need 1)", p)
	case mx != 0 || my != 0:
		return 0, 0, nil, fmt.Errorf("jbig: adaptive template MX=%d MY=%d not supported (need 0)", mx, my)
	case order&0xf0 != 0:
		return 0, 0, nil, fmt.Errorf("jbig: invalid order byte 0x%02x", order)
	case options&^optLRLTWO != 0:
		return 0, 0, nil, fmt.Errorf("jbig: options 0x%02x not supported (only LRLTWO 0x40; no TPBON/TPDON/DPON/VLENGTH)", options)
	case xd == 0 || yd == 0 || l0 == 0:
		return 0, 0, nil, fmt.Errorf("jbig: invalid image size %dx%d, L0=%d", xd, yd, l0)
	}
	stride := (uint64(xd) + 7) / 8
	if stride*uint64(yd) > maxImageBytes {
		return 0, 0, nil, fmt.Errorf("jbig: image %dx%d too large", xd, yd)
	}
	width, height = int(xd), int(yd)
	rows = make([]byte, int(stride)*height)
	twoLine := options&optLRLTWO != 0

	var cxs [1024]context
	var dec decoder
	pos := bihLen
	stripes := (uint64(yd) + uint64(l0) - 1) / uint64(l0)
	for s := uint64(0); s < stripes; s++ {
		if pos, err = skipComments(bie, pos); err != nil {
			return 0, 0, nil, err
		}
		var end int
		end, err = sdeEnd(bie, pos)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("jbig: stripe %d: %w", s, err)
		}
		switch bie[end+1] {
		case markerSDNORM:
		case markerNewLen:
			return 0, 0, nil, fmt.Errorf("jbig: stripe %d: NEWLEN marker not supported", s)
		case markerSDRST:
			return 0, 0, nil, fmt.Errorf("jbig: stripe %d: SDRST not supported (only SDNORM)", s)
		default:
			return 0, 0, nil, fmt.Errorf("jbig: stripe %d: unexpected marker FF %02X", s, bie[end+1])
		}
		dec.init(bie[pos:end])
		y0 := int(s * uint64(l0))
		y1 := min(height, y0+int(l0))
		for y := y0; y < y1; y++ {
			decodeLine(&dec, &cxs, rows, int(stride), width, y, twoLine)
		}
		pos = end + 2
	}

	// After the last stripe allow comments and padding only.
	if pos, err = skipComments(bie, pos); err != nil {
		return 0, 0, nil, err
	}
	if pos+1 < len(bie) && bie[pos] == markerEsc && bie[pos+1] == markerNewLen {
		return 0, 0, nil, errors.New("jbig: NEWLEN marker not supported")
	}
	for _, b := range bie[pos:] {
		if b != 0x00 && b != 0xff {
			return 0, 0, nil, fmt.Errorf("jbig: unexpected data after last stripe at offset %d", pos)
		}
	}
	return width, height, rows, nil
}

// decodeLine reconstructs line y. Pixels outside the image are 0.
func decodeLine(dec *decoder, cxs *[1024]context, rows []byte, stride, width, y int, twoLine bool) {
	cur := rows[y*stride : (y+1)*stride]
	var prev, prev2 []byte
	if y > 0 {
		prev = rows[(y-1)*stride : y*stride]
	}
	if y > 1 {
		prev2 = rows[(y-2)*stride : (y-1)*stride]
	}
	var acc byte
	if twoLine {
		// Previous line x-3..x+2, current line x-4..x-1 (as Encode).
		h1 := pixel(prev, 0, width)<<1 | pixel(prev, 1, width)
		var h0 uint32
		for x := 0; x < width; x++ {
			h1 = (h1<<1 | pixel(prev, x+2, width)) & 0x3f
			p := dec.decode(&cxs[h1<<4|h0])
			h0 = (h0<<1 | uint32(p)) & 0xf
			acc = acc<<1 | p
			if x&7 == 7 {
				cur[x>>3] = acc
			}
		}
	} else {
		// Three-line template: line y-2 x-1..x+1, line y-1 x-2..x+2,
		// current line x-2..x-1.
		h2 := pixel(prev2, 0, width)
		h1 := pixel(prev, 0, width)<<1 | pixel(prev, 1, width)
		var h0 uint32
		for x := 0; x < width; x++ {
			h2 = (h2<<1 | pixel(prev2, x+1, width)) & 0x7
			h1 = (h1<<1 | pixel(prev, x+2, width)) & 0x1f
			p := dec.decode(&cxs[h2<<7|h1<<2|h0])
			h0 = (h0<<1 | uint32(p)) & 0x3
			acc = acc<<1 | p
			if x&7 == 7 {
				cur[x>>3] = acc
			}
		}
	}
	if width&7 != 0 {
		cur[width>>3] = acc << (8 - uint(width&7))
	}
}

// sdeEnd returns the offset of the marker (FF xx, xx != 00) ending the
// stripe data entity that starts at pos.
func sdeEnd(b []byte, pos int) (int, error) {
	for i := pos; i < len(b); i++ {
		if b[i] != markerEsc {
			continue
		}
		if i+1 >= len(b) {
			break
		}
		if b[i+1] != markerStuff {
			return i, nil
		}
		i++
	}
	return 0, errors.New("missing end-of-stripe marker (truncated stream)")
}

// skipComments skips COMMENT marker segments (FF 07, 4-byte length, data).
func skipComments(b []byte, pos int) (int, error) {
	for pos+1 < len(b) && b[pos] == markerEsc && b[pos+1] == markerComment {
		if pos+6 > len(b) {
			return 0, errors.New("jbig: truncated COMMENT marker")
		}
		n := uint64(binary.BigEndian.Uint32(b[pos+2:]))
		if uint64(pos)+6+n > uint64(len(b)) {
			return 0, errors.New("jbig: truncated COMMENT marker")
		}
		pos += 6 + int(n)
	}
	return pos, nil
}
