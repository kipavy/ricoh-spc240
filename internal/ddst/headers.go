// Package ddst writes the Ricoh DDST print stream (see docs/protocol.md).
// All multi-byte integers are big-endian. Layouts reproduce the official
// Windows driver byte for byte.
package ddst

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// JIDG is the end-of-job marker.
var JIDG = []byte("JIDG")

// JobHeader is the GDIJ record (120 bytes).
type JobHeader struct {
	Title           string
	Color           bool
	BPP             int // bits per pixel: 1, 2 or 4
	Duplex, Collate bool
	Copies          int // printer-side copies (uncollated); 0 means 1
}

// Bytes encodes the GDIJ record (Windows layout: no timestamp).
func (h JobHeader) Bytes() []byte {
	b := make([]byte, 120)
	copy(b, "GDIJ")
	binary.BigEndian.PutUint32(b[4:], 120)
	binary.BigEndian.PutUint16(b[8:], 1)
	copies := h.Copies
	if copies < 1 {
		copies = 1
	}
	binary.BigEndian.PutUint16(b[10:], uint16(copies))
	var flags uint16
	if h.Duplex {
		flags |= 0x0200
	}
	if h.Collate {
		flags |= 0x0008
	}
	binary.BigEndian.PutUint16(b[16:], flags)
	binary.BigEndian.PutUint16(b[18:], 168) // length of GJET
	if h.Color {
		b[24] = 1
	}
	b[32] = 1
	if h.BPP > 1 {
		b[33] = byte(h.BPP - 1)
	}
	binary.BigEndian.PutUint16(b[34:], 2)
	copy(b[56:120], h.Title) // truncated to 64 bytes, NUL padded
	return b
}

// GJETBytes returns the GJET job extension (168 bytes, Windows layout:
// empty host/user, bytes 148-151 = 01).
func GJETBytes() []byte {
	b := make([]byte, 168)
	copy(b, "GJET")
	binary.BigEndian.PutUint32(b[4:], 168)
	copy(b[148:], []byte{1, 1, 1, 1})
	return b
}

// Side is the GDIP side code.
type Side byte

const (
	SideSimplex Side = 1
	SideFront   Side = 5
	SideBack    Side = 0x0D
)

// PageHeader is the GDIP record (64 bytes).
type PageHeader struct {
	Paper             PaperSize
	Tray              Tray
	Type              PaperType
	WidthBits, Height int
	Planes            int
	Side              Side
	PageNo            int // 1-based
	Bands             int
	MediaUM           [2]uint32
}

// Bytes encodes the GDIP record.
func (p PageHeader) Bytes() []byte {
	b := make([]byte, 64)
	copy(b, "GDIP")
	binary.BigEndian.PutUint32(b[4:], 64)
	b[8] = p.Paper.Code
	b[9] = byte(p.Tray)
	b[10] = byte(p.Type)
	binary.BigEndian.PutUint16(b[12:], uint16(p.WidthBits))
	binary.BigEndian.PutUint16(b[14:], uint16(p.Height))
	for i := 0; i < 8; i++ { // Windows driver: eight 16-bit values of 100
		binary.BigEndian.PutUint16(b[16+2*i:], 100)
	}
	b[32] = byte(p.Planes)
	b[33] = byte(p.Side)
	binary.BigEndian.PutUint16(b[34:], uint16(p.PageNo))
	binary.BigEndian.PutUint16(b[54:], uint16(p.Bands))
	binary.BigEndian.PutUint32(b[56:], p.MediaUM[0])
	binary.BigEndian.PutUint32(b[60:], p.MediaUM[1])
	return b
}

// BandHeader is the GDIB record (32 bytes) preceding the K, Y, M, C data.
type BandHeader struct {
	First, Last, BlankPage bool
	Lens                   [4]uint32 // K, Y, M, C data lengths
	WidthBits, Lines       int
}

// Bytes encodes the GDIB record.
func (b BandHeader) Bytes() []byte {
	o := make([]byte, 32)
	copy(o, "GDIB")
	if b.First {
		o[7] |= 1
	}
	if b.Last {
		o[7] |= 2
	}
	for i, n := range b.Lens {
		binary.BigEndian.PutUint32(o[8+4*i:], n)
	}
	binary.BigEndian.PutUint16(o[24:], uint16(b.WidthBits))
	binary.BigEndian.PutUint16(o[26:], uint16(b.Lines))
	if b.BlankPage {
		o[30] = 1
	}
	return o
}

// PaperSize is one entry of the paper size table (protocol 5.1).
type PaperSize struct {
	Name              string
	Code              byte
	WidthPt, HeightPt float64
	MediaUM           [2]uint32 // width, height in micrometres
	Duplex            bool      // false if the driver refuses duplex for it
}

// paper builds an entry whose GDIP media size equals its nominal size.
func paper(name string, code byte, wUM, hUM uint32, duplex bool) PaperSize {
	return paperMedia(name, code, wUM, hUM, wUM, hUM, duplex)
}

// paperMedia builds an entry whose GDIP media size (as written by Windows)
// differs from the nominal size. Points are exact (unrounded) nominal sizes,
// so PrintableArea gives the Windows raster size (A4: 4768 x 6816).
func paperMedia(name string, code byte, wNom, hNom, wUM, hUM uint32, duplex bool) PaperSize {
	pt := func(um uint32) float64 { return float64(um) / 25400 * 72 }
	return PaperSize{name, code, pt(wNom), pt(hNom), [2]uint32{wUM, hUM}, duplex}
}

// A4, A5 and Letter are taken from Windows captures; the rest is nominal.
var papers = []PaperSize{
	paper("Letter", 0x01, 215900, 279400, true),
	paper("Legal", 0x05, 215900, 355600, true),
	paper("Executive", 0x07, 184150, 266700, true),
	paper("HalfLetter", 0x06, 139700, 215900, false),
	paperMedia("A4", 0x09, 210000, 297000, 209974, 296926, true),
	paperMedia("A5", 0x0B, 148000, 210000, 147998, 209974, false),
	paper("A6", 0x46, 105000, 148000, false),
	paper("B5", 0x0D, 182000, 257000, true), // JIS B5
	paper("B6", 0x58, 128000, 182000, false),
	paper("FS", 0x10, 215900, 330200, true),
	paper("Folio", 0x0F, 210000, 330000, true),
	paper("Foolscap", 0x0E, 203200, 330200, true),
	paper("Env10", 0x14, 104775, 241300, false),
	paper("Monarch", 0x25, 98425, 190500, false),
	paper("EnvDL", 0x1B, 110000, 220000, false),
	paper("EnvC5", 0x1C, 162000, 229000, false),
	paper("EnvC6", 0x1F, 114000, 162000, false),
	paper("16K", 0x5D, 195000, 270000, true),
	paper("Postcard", 0x2B, 100000, 148000, false),
	paper("ReplyPostcard", 0x45, 148000, 200000, false),
	paper("Custom", 0xFF, 0, 0, false),
}

// normalize lower-cases and drops separators so "Half Letter", "half-letter"
// and "halfletter" match.
func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '_', '#':
			return -1
		}
		return r
	}, strings.ToLower(s))
}

// LookupPaper finds a paper size by name (case-insensitive).
func LookupPaper(name string) (PaperSize, error) {
	n := normalize(name)
	for _, p := range papers {
		if normalize(p.Name) == n {
			return p, nil
		}
	}
	return PaperSize{}, fmt.Errorf("unknown paper size %q", name)
}

// PaperType is the GDIP paper type code (protocol 5.2).
type PaperType byte

var paperTypes = map[string]PaperType{
	"plainrecycled": 0x00, "plain": 0x01, "plain90": 0x10, "recycled": 0x02,
	"color": 0x03, "preprinted": 0x05, "prepunched": 0x06, "thick": 0x0C,
	"thick160": 0x0D, "letterhead": 0x04, "bond": 0x08, "cardstock": 0x09,
	"labels": 0x07, "envelope": 0x0E, "thin": 0x0F, "thinner": 0x12,
}

// LookupPaperType finds a paper type by name (case-insensitive).
func LookupPaperType(name string) (PaperType, error) {
	if t, ok := paperTypes[normalize(name)]; ok {
		return t, nil
	}
	return 0, fmt.Errorf("unknown paper type %q", name)
}

// PaperTypeAllowsDuplex reports whether the driver accepts duplex on t.
func PaperTypeAllowsDuplex(t PaperType) bool {
	switch t {
	case 0x10, 0x0C, 0x0D, 0x08, 0x09, 0x07, 0x0E: // Plain90, Thick, Thick160, Bond, Cardstock, Labels, Envelope
		return false
	}
	return true
}

// Tray is the GDIP input tray code.
type Tray byte

// LookupTray finds an input tray by name (case-insensitive).
func LookupTray(name string) (Tray, error) {
	switch normalize(name) {
	case "auto":
		return 0, nil
	case "bypass", "manual":
		return 1, nil
	case "tray1":
		return 2, nil
	case "tray2":
		return 3, nil
	}
	return 0, fmt.Errorf("unknown tray %q", name)
}
