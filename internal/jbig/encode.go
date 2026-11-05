// Package jbig implements the single-layer subset of JBIG1 (ITU-T T.82 /
// T.85) used by the printer: one resolution layer, one bit plane, 256-line
// stripes, the two-line template, no typical prediction and no adaptive
// template moves. Output is byte-identical to the jbigkit reference encoder
// run as `pbmtojbg -q -s 256 -m 0 -p 64 -o 3`.
package jbig

import "encoding/binary"

const (
	stripeLines = 256 // L0
	bihLen      = 20

	optLRLTWO = 0x40

	markerEsc     = 0xff
	markerStuff   = 0x00
	markerSDNORM  = 0x02
	markerSDRST   = 0x03
	markerNewLen  = 0x05
	markerComment = 0x07
)

// qmState is one row of the probability estimation table (T.82 Table 24).
type qmState struct {
	lsz        uint32
	nmps, nlps uint8
	swtch      bool
}

var qmTable = [113]qmState{
	{0x5a1d, 1, 1, true}, {0x2586, 2, 14, false}, {0x1114, 3, 16, false}, {0x080b, 4, 18, false},
	{0x03d8, 5, 20, false}, {0x01da, 6, 23, false}, {0x00e5, 7, 25, false}, {0x006f, 8, 28, false},
	{0x0036, 9, 30, false}, {0x001a, 10, 33, false}, {0x000d, 11, 35, false}, {0x0006, 12, 9, false},
	{0x0003, 13, 10, false}, {0x0001, 13, 12, false}, {0x5a7f, 15, 15, true}, {0x3f25, 16, 36, false},
	{0x2cf2, 17, 38, false}, {0x207c, 18, 39, false}, {0x17b9, 19, 40, false}, {0x1182, 20, 42, false},
	{0x0cef, 21, 43, false}, {0x09a1, 22, 45, false}, {0x072f, 23, 46, false}, {0x055c, 24, 48, false},
	{0x0406, 25, 49, false}, {0x0303, 26, 51, false}, {0x0240, 27, 52, false}, {0x01b1, 28, 54, false},
	{0x0144, 29, 56, false}, {0x00f5, 30, 57, false}, {0x00b7, 31, 59, false}, {0x008a, 32, 60, false},
	{0x0068, 33, 62, false}, {0x004e, 34, 63, false}, {0x003b, 35, 32, false}, {0x002c, 9, 33, false},
	{0x5ae1, 37, 37, true}, {0x484c, 38, 64, false}, {0x3a0d, 39, 65, false}, {0x2ef1, 40, 67, false},
	{0x261f, 41, 68, false}, {0x1f33, 42, 69, false}, {0x19a8, 43, 70, false}, {0x1518, 44, 72, false},
	{0x1177, 45, 73, false}, {0x0e74, 46, 74, false}, {0x0bfb, 47, 75, false}, {0x09f8, 48, 77, false},
	{0x0861, 49, 78, false}, {0x0706, 50, 79, false}, {0x05cd, 51, 48, false}, {0x04de, 52, 50, false},
	{0x040f, 53, 50, false}, {0x0363, 54, 51, false}, {0x02d4, 55, 52, false}, {0x025c, 56, 53, false},
	{0x01f8, 57, 54, false}, {0x01a4, 58, 55, false}, {0x0160, 59, 56, false}, {0x0125, 60, 57, false},
	{0x00f6, 61, 58, false}, {0x00cb, 62, 59, false}, {0x00ab, 63, 61, false}, {0x008f, 32, 61, false},
	{0x5b12, 65, 65, true}, {0x4d04, 66, 80, false}, {0x412c, 67, 81, false}, {0x37d8, 68, 82, false},
	{0x2fe8, 69, 83, false}, {0x293c, 70, 84, false}, {0x2379, 71, 86, false}, {0x1edf, 72, 87, false},
	{0x1aa9, 73, 87, false}, {0x174e, 74, 72, false}, {0x1424, 75, 72, false}, {0x119c, 76, 74, false},
	{0x0f6b, 77, 74, false}, {0x0d51, 78, 75, false}, {0x0bb6, 79, 77, false}, {0x0a40, 48, 77, false},
	{0x5832, 81, 80, true}, {0x4d1c, 82, 88, false}, {0x438e, 83, 89, false}, {0x3bdd, 84, 90, false},
	{0x34ee, 85, 91, false}, {0x2eae, 86, 92, false}, {0x299a, 87, 93, false}, {0x2516, 71, 86, false},
	{0x5570, 89, 88, true}, {0x4ca9, 90, 95, false}, {0x44d9, 91, 96, false}, {0x3e22, 92, 97, false},
	{0x3824, 93, 99, false}, {0x32b4, 94, 99, false}, {0x2e17, 86, 93, false}, {0x56a8, 96, 95, true},
	{0x4f46, 97, 101, false}, {0x47e5, 98, 102, false}, {0x41cf, 99, 103, false}, {0x3c3d, 100, 104, false},
	{0x375e, 93, 99, false}, {0x5231, 102, 105, false}, {0x4c0f, 103, 106, false}, {0x4639, 104, 107, false},
	{0x415e, 99, 103, false}, {0x5627, 106, 105, true}, {0x50e7, 107, 108, false}, {0x4b85, 103, 109, false},
	{0x5597, 109, 110, false}, {0x504f, 107, 111, false}, {0x5a10, 111, 110, true}, {0x5522, 109, 112, false},
	{0x59eb, 111, 112, true},
}

// context holds the adaptive state of one context: table index and MPS.
type context struct {
	st  uint8
	mps uint8
}

// encoder is the QM arithmetic encoder of T.82 §6.8 (software conventions).
type encoder struct {
	out    []byte
	c      uint32
	a      uint32
	ct     int
	buffer int // pending output byte, -1 if none (may still receive a carry)
	sc     int // number of pending 0xff bytes after buffer
	keep   int // output length up to the last non-zero byte of this stripe
}

func (e *encoder) init() {
	e.c, e.a, e.ct, e.buffer, e.sc = 0, 0x10000, 11, -1, 0
	e.keep = len(e.out)
}

// emit appends a coded data byte, escaping 0xff as FF 00.
func (e *encoder) emit(b byte) {
	e.out = append(e.out, b)
	if b == markerEsc {
		e.out = append(e.out, markerStuff)
	}
	if b != 0 {
		e.keep = len(e.out)
	}
}

func (e *encoder) encode(cx *context, pix uint8) {
	s := &qmTable[cx.st]
	lsz := s.lsz
	e.a -= lsz
	if pix != cx.mps {
		// LPS takes the upper sub-interval unless it is the larger one
		// (conditional exchange).
		if e.a >= lsz {
			e.c += e.a
			e.a = lsz
		}
		if s.swtch {
			cx.mps ^= 1
		}
		cx.st = s.nlps
	} else {
		if e.a >= 0x8000 {
			return
		}
		if e.a < lsz {
			e.c += e.a
			e.a = lsz
		}
		cx.st = s.nmps
	}
	for e.a < 0x8000 {
		e.a <<= 1
		e.c <<= 1
		e.ct--
		if e.ct == 0 {
			e.byteOut()
		}
	}
}

// byteOut moves the top byte of C to the output, resolving carries through
// the buffered byte and pending 0xff run.
func (e *encoder) byteOut() {
	t := e.c >> 19
	switch {
	case t > 0xff: // carry into buffered bytes: buffer+1, then 0xff run becomes zeros
		if e.buffer >= 0 {
			e.emit(byte(e.buffer + 1))
		}
		for ; e.sc > 0; e.sc-- {
			e.emit(0x00)
		}
		e.buffer = int(t & 0xff)
	case t == 0xff:
		e.sc++
	default:
		if e.buffer >= 0 {
			e.emit(byte(e.buffer))
		}
		for ; e.sc > 0; e.sc-- {
			e.emit(0xff)
		}
		e.buffer = int(t)
	}
	e.c &= 0x7ffff
	e.ct = 8
}

// flush terminates the stripe's coded data, then drops every trailing zero
// byte of it (allowed by T.82: the decoder reads zeros past the end; jbigkit
// does the same).
func (e *encoder) flush() {
	// Pick the value in [C, C+A) with the most trailing zero bits.
	t := (e.a - 1 + e.c) & 0xffff0000
	if t < e.c {
		e.c = t + 0x8000
	} else {
		e.c = t
	}
	e.c <<= uint(e.ct)
	if e.c&0xf8000000 != 0 { // final carry
		if e.buffer >= 0 {
			e.emit(byte(e.buffer + 1))
		}
		for ; e.sc > 0; e.sc-- {
			e.emit(0x00)
		}
	} else {
		if e.buffer >= 0 {
			e.emit(byte(e.buffer))
		}
		for ; e.sc > 0; e.sc-- {
			e.emit(0xff)
		}
	}
	e.emit(byte(e.c >> 19))
	e.emit(byte(e.c >> 11))
	e.out = e.out[:e.keep]
}

// pixel returns bit x of row (0 outside [0, width)).
func pixel(row []byte, x, width int) uint32 {
	if row == nil || x < 0 || x >= width {
		return 0
	}
	return uint32(row[x>>3]>>(7-uint(x&7))) & 1
}

// Encode a single-layer bi-level image: width in pixels (= bits), rows of
// (width+7)/8 bytes, MSB first, 1 = ink. Width and height must be >= 1.
// BIH: DL=0 D=0 P=1, XD=width, YD=height, L0=256, MX=0 MY=0, order=0x03,
// options=0x40 (LRLTWO). Each 256-line stripe ends with FF 02 (SDNORM).
func Encode(width, height int, rows []byte) []byte {
	return encodeImage(width, height, stripeLines, rows, optLRLTWO)
}

// EncodeStripe is Encode with the stripe height L0 chosen by the caller
// (l0 >= 1). The Windows driver writes L0 = min(256, height) for every band.
// The arithmetic-coded data equal Encode's whenever both give one stripe.
func EncodeStripe(width, height, l0 int, rows []byte) []byte {
	return encodeImage(width, height, l0, rows, optLRLTWO)
}

// encodeImage encodes with the two-line (options 0x40) or three-line
// (options 0x00, used only by tests to exercise Decode) template.
func encodeImage(width, height, l0 int, rows []byte, options byte) []byte {
	stride := (width + 7) / 8
	if width < 1 || height < 1 || l0 < 1 || uint64(l0) > 0xffffffff || uint64(width) > 0xffffffff || uint64(height) > 0xffffffff || len(rows) < stride*height {
		panic("jbig: invalid image dimensions or short pixel buffer")
	}
	out := make([]byte, bihLen, bihLen+len(rows)/8+64)
	out[2] = 1 // P
	binary.BigEndian.PutUint32(out[4:], uint32(width))
	binary.BigEndian.PutUint32(out[8:], uint32(height))
	binary.BigEndian.PutUint32(out[12:], uint32(l0))
	out[18] = 0x03 // order: ILEAVE | SMID
	out[19] = options
	twoLine := options&optLRLTWO != 0

	var cxs [1024]context
	e := encoder{out: out}
	for y0 := 0; y0 < height; y0 += l0 {
		e.init()
		for y := y0; y < height && y < y0+l0; y++ {
			cur := rows[y*stride : (y+1)*stride]
			var prev, prev2 []byte
			if y > 0 {
				prev = rows[(y-1)*stride : y*stride]
			}
			if y > 1 {
				prev2 = rows[(y-2)*stride : (y-1)*stride]
			}
			if twoLine {
				// Previous line x-3..x+2, current line x-4..x-1.
				h1 := pixel(prev, 0, width)<<1 | pixel(prev, 1, width)
				var h0 uint32
				for x := 0; x < width; x++ {
					h1 = (h1<<1 | pixel(prev, x+2, width)) & 0x3f
					p := pixel(cur, x, width)
					e.encode(&cxs[h1<<4|h0], uint8(p))
					h0 = (h0<<1 | p) & 0xf
				}
				continue
			}
			// Line y-2 x-1..x+1, line y-1 x-2..x+2, current line x-2..x-1.
			h2 := pixel(prev2, 0, width)
			h1 := pixel(prev, 0, width)<<1 | pixel(prev, 1, width)
			var h0 uint32
			for x := 0; x < width; x++ {
				h2 = (h2<<1 | pixel(prev2, x+1, width)) & 0x7
				h1 = (h1<<1 | pixel(prev, x+2, width)) & 0x1f
				p := pixel(cur, x, width)
				e.encode(&cxs[h2<<7|h1<<2|h0], uint8(p))
				h0 = (h0<<1 | p) & 0x3
			}
		}
		e.flush()
		e.out = append(e.out, markerEsc, markerSDNORM)
	}
	return e.out
}
