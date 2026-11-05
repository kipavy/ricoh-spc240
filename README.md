# ricoh-spc240

A small Go driver for the Ricoh Aficio SP C240DN colour laser printer. It
renders a document (PDF, PostScript, text or image), halftones it and sends the
printer's native DDST stream straight to TCP port 9100. No CUPS and no Ricoh
driver is needed. It ships as a disposable Docker image,
`ghcr.io/kipavy/ricoh-spc240`, that contains Ghostscript, img2pdf, enscript and
the Ricoh data files the print pipeline needs.

## Quick start

```sh
docker run --rm -v "$PWD":/data ghcr.io/kipavy/ricoh-spc240 \
  print /data/file.pdf --printer 192.168.1.50
```

The container runs as an unprivileged user (uid 65534), so the mounted
directory must be readable by it, and writable if you use `--output`
(or pass `--user "$(id -u)"`).

## Commands

```
ricoh-spc240 print FILE         render FILE and send it to the printer (or --output FILE.ddst)
ricoh-spc240 encode PAGE...     encode PAM/PGM page rasters (already page-sized) into a job
ricoh-spc240 testpage           one-sheet duplex paper test (needs --output or --printer)
ricoh-spc240 decode FILE.ddst   list the records of a stream
```

Flags may appear before or after the positional arguments.

### Flags of `print` and `encode`

| Flag | Default | Meaning |
|---|---|---|
| `--printer HOST[:PORT]` | required (or `$RICOH_PRINTER`), unless `--output` is used | printer address, port 9100 by default |
| `--output FILE` | | write the job to a file instead of printing (exclusive with `--printer`) |
| `--mono` | off | print in black and white |
| `--duplex` | `none` | `none`, `long` or `short` (binding edge) |
| `--copies N` | `1` | number of copies (1 to 999) |
| `--no-collate` | off | do not collate copies (ignored with `--duplex`, see below) |
| `--pages` | all | page selection, e.g. `1-3,5` |
| `--paper` | `A4` | paper size (list below) |
| `--type` | `plainrecycled` | paper type (list below) |
| `--tray` | `auto` | `auto`, `bypass` (or `manual`), `tray1`, `tray2` |
| `--quality` | `fast` | `fast`, `standard` or `fine` (halftone screen set) |
| `--gray-k` | off | print neutral colours with black toner only |
| `--landscape` | off | print landscape documents full page (see below) |
| `--assets DIR` | `/usr/share/ricoh-spc240` | Ricoh data dir (or `$RICOH_ASSETS`) |

Paper sizes: A4, A5, A6, B5, B6, Letter, Legal, Executive, HalfLetter, FS,
Folio, Foolscap, Env10, Monarch, EnvDL, EnvC5, EnvC6, 16K, Postcard,
ReplyPostcard. The SP C240DN feeds up to Legal, so A3, B4 and Ledger are not
offered.

Paper types: plainrecycled, plain, plain90, recycled, color, preprinted,
prepunched, thick, thick160, letterhead, bond, cardstock, labels, envelope,
thin, thinner.

Duplex is refused for paper sizes and paper types the printer cannot duplex.
With `--duplex`, copies are always collated: `--no-collate` is ignored (a
warning is printed on stderr).

`encode` takes ready-made page rasters, so it accepts every flag above except
`--landscape`, which it rejects (usage error), and `--pages`, which it
validates but otherwise ignores.

Exit codes: 0 success, 1 runtime failure (I/O, rendering, printer connection),
2 usage error (bad flag or option value).

Text files are converted to Latin-1 before printing; characters outside
Latin-1 print as `?`.

### Landscape documents

A landscape page is fed through the printer in portrait. With `--landscape`,
`print` renders every page on landscape media and rotates it so that it fills
the sheet. The top of the content ends up on the right-hand long edge when the
sheet is held in portrait: turn the sheet 90 degrees counter-clockwise to read
it. Without the flag a landscape document is shrunk into the middle of the
portrait sheet.

What `--landscape` does depends on the input: portrait PDF and PostScript pages
print unchanged (only landscape pages are rotated); images are always placed
landscape; text is always laid out landscape.

Binding: a notepad-style landscape document (bound along the landscape page's
top, which is a long edge of the sheet) needs `--duplex long`; a book-style one
(bound along the landscape short edge) needs `--duplex short`. `encode` and
`testpage` reject `--landscape` (pages are already rasters / fixed layout).

### Flags of `decode`

| Flag | Default | Meaning |
|---|---|---|
| `--png DIR` | | also write `page-NN.png` previews into DIR |

## Examples

```sh
alias ricoh='docker run --rm -v "$PWD":/data ghcr.io/kipavy/ricoh-spc240'

ricoh print /data/report.pdf --printer 192.168.1.50 --duplex long
ricoh print /data/report.pdf --printer 192.168.1.50 --mono --pages 1-3,5 --copies 2
ricoh print /data/notes.txt  --printer 192.168.1.50
ricoh print /data/photo.jpg  --printer 192.168.1.50 --quality fine

# Write the job to a file instead of printing, then inspect it
ricoh print /data/report.pdf --output /data/report.ddst
ricoh decode /data/report.ddst --png /data/preview
```

Pass `-e RICOH_PRINTER=192.168.1.50` to `docker run` to omit `--printer`.
Accepted inputs: PDF, PostScript, UTF-8 text, and JPEG, PNG, GIF, TIFF, BMP.

## Test sheet

```sh
ricoh testpage --printer 192.168.1.50
```

Prints exactly one A4 sheet, duplex, with less than 3 % ink coverage, to check
colours, alignment and back-side orientation. It never falls back to the default
printer: an explicit `--printer` or `--output FILE` is required, and layout flags
(`--paper`, `--duplex`, ...) are rejected.

## How it works

`print` converts the input to PDF (Ghostscript, img2pdf or enscript), renders
it at 600 dpi through Ghostscript, applies the Ricoh ICC profile for colour,
halftones each plane with Ricoh's screens, and encodes the result as a DDST job
that is streamed to the printer. The wire format is documented in
[docs/protocol.md](docs/protocol.md).

## Legal note

This project was written for interoperability, as permitted by article 6 of EU
Directive 2009/24/EC. No Ricoh code is part of this repository, and the protocol
notes are written in our own words.

The halftone screens and the ICC profile are Ricoh's data. They are not stored
in this repository: `scripts/fetch-ricoh-assets.sh` downloads Ricoh's official
macOS driver from support.ricoh.com at image build time, verifies its SHA-256
and extracts those files, and the published image includes them. They remain
Ricoh's property. If you do not want to redistribute them, build the image
yourself (`docker build -t ricoh-spc240 .`) and keep it private.

## Credits

- `rastertoddst` by Malte Sartor, for early notes on the protocol.
- jbigkit, used only as a reference decoder in tests.

## Status

Developed against a Ricoh Aficio SP C240DN. The siblings (SP C240SF, C242DN,
C242SF) probably speak the same protocol but are untested. Colour, monochrome
and duplex printing are implemented. Known gaps: only the A4, A5 and Letter
raster geometries come from real captures, the other paper sizes are nominal;
there is no printer status readback; only raw TCP on port 9100 is supported.
