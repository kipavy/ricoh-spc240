package ddst

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Record is one record of a DDST stream.
type Record struct {
	Kind   string    // GDIJ, GJET, GDIP, GDIB, JIDG or GDIC
	Offset int       // byte offset of the record in the stream
	Header []byte    // the fixed-size record header
	Planes [4][]byte // GDIB only: raw K, Y, M, C JBIG streams (padding included)
}

// recordSize is the fixed header size of every record kind.
var recordSize = map[string]int{
	"GDIJ": 120, "GJET": 168, "GDIP": 64, "GDIB": 32, "JIDG": 4, "GDIC": 16,
}

// Parse splits a DDST stream into records. On malformed input it returns
// the records parsed so far and an error naming the offending offset. The
// stream must end with JIDG (or a GDIC cancel).
func Parse(r io.Reader) ([]Record, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("ddst: reading stream: %w", err)
	}
	var recs []Record
	off := 0
	for off < len(data) {
		if len(data)-off < 4 {
			return recs, fmt.Errorf("ddst: truncated record at offset %d", off)
		}
		kind := string(data[off : off+4])
		n, ok := recordSize[kind]
		if !ok {
			return recs, fmt.Errorf("ddst: unknown record %q at offset %d", kind, off)
		}
		if len(data)-off < n {
			return recs, fmt.Errorf("ddst: truncated %s at offset %d (%d of %d bytes)", kind, off, len(data)-off, n)
		}
		rec := Record{Kind: kind, Offset: off, Header: data[off : off+n : off+n]}
		switch kind {
		case "GDIJ", "GJET", "GDIP":
			if l := binary.BigEndian.Uint32(data[off+4:]); l != uint32(n) {
				return recs, fmt.Errorf("ddst: %s at offset %d declares length %d, want %d", kind, off, l, n)
			}
		}
		off += n
		if kind == "GDIB" {
			for k := 0; k < 4; k++ {
				l := uint64(binary.BigEndian.Uint32(rec.Header[8+4*k:]))
				if l > uint64(len(data)-off) {
					return recs, fmt.Errorf("ddst: GDIB at offset %d: %c data of %d bytes overruns the stream (%d left)",
						rec.Offset, "KYMC"[k], l, len(data)-off)
				}
				rec.Planes[k] = data[off : off+int(l) : off+int(l)]
				off += int(l)
			}
		}
		recs = append(recs, rec)
		if kind == "JIDG" || kind == "GDIC" {
			if off != len(data) {
				return recs, fmt.Errorf("ddst: %d unexpected bytes after %s at offset %d", len(data)-off, kind, off)
			}
			return recs, nil
		}
	}
	return recs, fmt.Errorf("ddst: stream ends at offset %d without JIDG", off)
}
