# Ricoh SP C240 series — DDST print stream

This documents the wire format accepted by the Ricoh Aficio SP C240DN (and very
likely its siblings SP C240SF / C242DN / C242SF). It was established for
interoperability by analysing Ricoh's official macOS driver v2.05
(`RicohAficioSPC240DNFilter`, CUPS raster filter) and is validated against
captures of the official Windows DDST driver v1.14. It is written in our own
words; no Ricoh code is reproduced.

Status markers: **[mac]** = read from the macOS filter, **[win]** = confirmed on
Windows driver captures, **[open]** = still to confirm.

## 1. Transport

Raw TCP to port 9100 (the printer has no PJL readback, IPP only advertises
`application/octet-stream`). Anything that is not this stream is silently
discarded. All multi-byte integers are **big-endian**.

## 2. Stream layout

```
GDIJ (120 B)  job header
GJET (168 B)  job extension (host / user)
  for each sheet side, in transmission order (see §6):
    GDIP (64 B)  page header
    GDIB (32 B) + K data + Y data + M data + C data   (one per band of 256 lines)
    ...
JIDG (4 B)    end of job
```

Cancel: the 16 bytes `"GDIC CANCEL     "` are sent instead of finishing. [mac]

## 3. GDIJ — job header (120 bytes) [mac]

| Offset | Size | Value |
|---|---|---|
| 0 | 4 | `"GDIJ"` |
| 4 | 4 | 120 (header length) |
| 8 | 2 | 100 (constant) |
| 10 | 2 | number of copies |
| 12 | 4 | 0 |
| 16 | 2 | flags: `0x0200` = duplex, `0x0008` = collate |
| 18 | 2 | 168 (length of the following GJET) |
| 20 | 4 | timestamp (§3.1) |
| 24 | 1 | colour mode: 1 = colour (4 planes), 0 = mono |
| 25 | 7 | 0 |
| 32 | 1 | 1 (constant) |
| 33 | 1 | bits per pixel − 1 (0, 1 or 3) |
| 34 | 2 | 2 (constant) |
| 36 | 20 | 0 |
| 56 | 64 | job title, NUL padded |

**Windows driver v1.14 differences [win]:** offset 8 = 1 (not 100), timestamp
0, copies always 1 (copies are sent as repeated pages, collate flag `0x0008`
set), host/user empty, GJET bytes 148–151 = `01 01 01 01`. Both variants are
self-consistent. **Our writer reproduces the Windows layout byte for byte**
(GDIJ, GJET, GDIP, GDIB, JBIG options and padding), so that its output can be
compared exactly with captures of the official Windows driver. Only the job
title differs.

### 3.1 Timestamp
`day<<27 | month<<23 | (year−1980)<<16 | (second/2)<<11 | minute<<5 | hour`
(month 1–12). [mac — taken from the disassembly of `-[NSCalendarDate destinyDate]`]

## 4. GJET — job extension (168 bytes) [mac]

| Offset | Size | Value |
|---|---|---|
| 0 | 4 | `"GJET"` |
| 4 | 4 | 168 |
| 8 | 64 | host name |
| 72 | 64 | user name |
| 136 | 16 | 0 |
| 152 | 16 | user code (Ricoh "user code" accounting option, usually empty) |

## 5. GDIP — page header (64 bytes) [mac]

| Offset | Size | Value |
|---|---|---|
| 0 | 4 | `"GDIP"` |
| 4 | 4 | 64 |
| 8 | 1 | paper size code (§5.1) |
| 9 | 1 | input tray: 0 auto, 1 bypass (manual), 2 tray 1, 3 tray 2, 4 tray 3, 5 tray 4 |
| 10 | 1 | paper type (§5.2) |
| 11 | 1 | 0 |
| 12 | 2 | row width **in bits**: `roundup(roundup(width_px × bpp, 8), 128)` |
| 14 | 2 | page height in lines |
| 16 | 16 | 0 |
| 32 | 1 | number of planes: 1 (mono) or 4 (CMYK) |
| 33 | 1 | side: 1 = simplex, 5 = duplex front, 0x0D = duplex back |
| 34 | 2 | page number (0-based, see §6 for duplex) |
| 36 | 18 | 0 |
| 54 | 2 | number of bands = ceil(height / 256) |
| 56 | 4 | media width in µm (= points / 72 × 25.4 × 1000, truncated) |
| 60 | 4 | media height in µm |

**Windows driver [win]:** bytes 16–31 hold eight 16-bit values of 100;
page numbers are **1-based** (simplex page n → n); the raster is the printable
area only: `width = roundup32(page_px − 200)`, `height ≈ page_px − 200`
(≈ 4.2 mm margin each side at 600 dpi). Measured: A4 4768 × 6816,
Letter 4928 × 6402, A5 3296 × 4762. The macOS driver sends the size CUPS gives
it (PPD imageable area).

**[win] phantom lines:** the declared height is **2 lines more** than the
rows actually encoded. With R real rows (`R = round(media height µm × 600 /
25400) − 200`: A4 6814, A5 4760, Letter 6400) the GDIP height is `R + 2`
and the band count `ceil(R / 256)` (A4 27, A5 19, Letter 25). The 2 extra
lines appear only in the declared size of the short band (§7); no JBIG
image encodes them. **Our choice:** same as Windows (`ddst.PageRaster`
gives W × R; A4 → 4768 × 6814 rows, declared 6816), page numbers 1-based.

### 5.1 Paper size codes
Letter 1, Legal 5, Ledger 0x11, Executive 7, Half Letter 6, A3 8, **A4 9**,
A5 0x0B, A6 0x46, B4 0x0C, JIS B5 0x0D, B6 0x58, FS 0x10, Folio 0x0F,
Foolscap 0x0E, Env #10 0x14, Monarch 0x25, Env DL 0x1B, Env C5 0x1C,
Env C6 0x1F, 8K 0x5C, 16K 0x5D, Postcard 0x2B, Reply-paid postcard 0x45,
custom 0xFF.

### 5.2 Paper type codes
PlainRecycled 0 (driver default), Plain 1, Plain90 (thick 1) 0x10,
Recycled 2, Color 3, Preprinted 5, Prepunched 6, Thick (thick 2) 0x0C,
Thick160 0x0D, Letterhead 4, Bond 8, Cardstock 9, Labels 7, Envelope 0x0E,
Thin 0x0F, Thinner 0x12.

Duplex is refused by the driver for A5, B6, A6, Half Letter, envelopes,
postcards, custom sizes, and for paper types Plain90, Thick, Thick160, Bond,
Cardstock, Labels, Envelope.

## 6. Page order and duplex [mac]

* Simplex: pages are sent in order, `side = 1`, `page number = index`.
* Duplex: pages are taken in pairs (index 2k = front, 2k+1 = back).
  * The **back (odd index) is transmitted first**, then the front: the driver
    buffers the whole front page and sends it right after the back page ends.
  * `side` = 5 for the front (even index), 0x0D for the back (odd index).
  * `page number` is swapped within the pair: front gets `index+1`, back gets
    `index−1`.
  * If the job has an odd number of sides, a blank page is appended so the last
    sheet has a back.
  * GDIJ flag `0x0200` is set.
* Rotation: the back side is **not** rotated for long-edge binding (the PPD
  does not set `DestinyFlipDuplex`). With the PPD option `Rotated=True` the page
  is rotated 180° (rows reversed and each row mirrored).
* **[win] confirmed:** back page sent first (`side 0x0D`, page number 2), then
  front (`side 5`, page number 1). For **long-edge** binding the driver
  **rotates the back page 180°** itself (there is no binding flag in the
  stream); **short-edge**
  back pages are not rotated. A rotated page is banded so that the short
  band comes **first**: it is the unrotated page's bands sent in reverse
  order, each rotated 180° (c2_color_long back: `1:160 0:256 … 2:256`,
  front `1:256 … 2:160`, as `flags:lines`). With an odd page count the
  Windows driver sends the last page as **simplex** (`side 1`) instead of
  adding a blank back. **Our choice:** follow Windows (1-based numbers, last
  odd page simplex, long-edge back rotated 180°).
* A page with a single plane (K only) is accepted inside a colour job [win:
  blank pages are sent with `planes = 1`].

## 7. GDIB — band header (32 bytes) + data [mac]

Bands are 256 lines high (last band shorter; see §7.1 for the Windows layout).

| Offset | Size | Value |
|---|---|---|
| 0 | 4 | `"GDIB"` |
| 4 | 3 | 0 |
| 7 | 1 | flags: bit 0 = first band of the page, bit 1 = last band |
| 8 | 4 | length of K data |
| 12 | 4 | length of Y data |
| 16 | 4 | length of M data |
| 20 | 4 | length of C data |
| 24 | 2 | row width in bits (same as GDIP offset 12) |
| 26 | 2 | lines in this band (declared; [win] short band = encoded rows + 2) |
| 28 | 2 | 0 |
| 30 | 1 | 1 on the last band if the whole page was **blank** (no non-zero raster byte), else 0 |
| 31 | 1 | 0 |

The band data follow immediately in the order **K, Y, M, C** (mono jobs send K
only, the other lengths are 0).

### 7.1 Plane data (JBIG)
Each plane of each band is an independent JBIG1 (ITU-T T.85) bi-level image:

* BIH (20 bytes): `DL=0, D=0, P=1, 0, XD=row width in bits, YD=lines in band,
  L0=256, MX=0, MY=0, order=0x03, options=0x48` (LRLTWO + TPBON).
* Multi-bit pixels are simply packed MSB-first into the bit plane (2 bpp → 2
  bits per pixel), so XD = width × bpp rounded as in §5.
* The stream (BIH + data) is zero-padded so that its length is
  `20 + 32·k` bytes. [mac]
* **[win]:** options byte 0x40 (LRLTWO only, no TPBON). The arithmetic-coded
  data are **identical to the reference encoder jbigkit**
  (`pbmtojbg -q -s 256 -m 0 -p 64 -o 3`) except that Windows keeps the
  coder's trailing zero bytes before the final `FF 02` (T.85 allows dropping
  them; jbigkit drops them). After `FF 02` Windows pads with `00` or `FF`
  bytes to a length that is a multiple of 8.
  **Our choice:** options 0x40, MX 0, standard T.85 output identical to
  jbigkit (trailing zeros dropped), then `00` padding to a multiple of 8.
* **[win] band heights:** bands hold 256 real rows except the remainder
  band (`R mod 256` rows, or 256 when R is a multiple of 256). The
  remainder band is the last one sent, or the first on a rotated long-edge
  back (§6). It **declares** `real + 2` lines in GDIB offset 26 while each
  of its JBIG images has `YD = real`: A4 160 / 158, A5 154 / 152, Letter
  (25 × 256 rows) 258 / 256 on its last band (Letter: 24 × 256 + 258).
  Flags stay bit 0 on the first band sent, bit 1 on the last, whatever
  their heights. **Our choice:** identical.

## 8. Raster preparation [mac]

* Resolution 600 × 600 dpi.
* Colour: CUPS raster CMYK, 8 bits per channel, chunky; mono: 8-bit K.
  Colour management is the job of the renderer (CUPS used the PPD's
  `RICOH Aficio SP C240DN CS.icc` profile).
* Optional "gray reproduction = K": any pixel with C = M = Y ≠ 0 is turned into
  pure K (`K = min(255, K + C)`, C = M = Y = 0).
* Halftoning with Ricoh's screen tables (§9). The screen set is selected by
  `Gradiation + PaperType + (Color|Mono)`, e.g. `FastPlainRecycledColor`.
  Gradiation: Fast → 1 bpp, Standard → 2 bpp, Fine → 4 bpp.

## 9. Halftone screen files

Text header `"<version> <width> <height> <input depth> <output depth>"` then a
NUL byte, then the table. Version 1, input depth 8 (256 levels), output depth
1, 2 or 4. The table holds `height × width × 256` output values packed MSB-first
(`depth` bits each), ordered **row, column, level**.

Halftoning pixel (x, y) of level v: `out = table[y mod height][x mod width][v]`,
written MSB-first into the plane row. One file per colorant (C, M, Y, K); the
mapping from screen-set name to files is in `ScreenSets.plist`.
