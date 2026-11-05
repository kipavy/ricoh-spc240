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
  print /data/file.pdf --printer 192.168.1.23
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
| `--printer HOST[:PORT]` | `192.168.1.23:9100` (or `$RICOH_PRINTER`) | printer address |
| `--output FILE` | | write the job to a file instead of printing (exclusive with `--printer`) |
| `--mono` | off | print in black and white |
| `--duplex` | `none` | `none`, `long` or `short` (binding edge) |
| `--copies N` | `1` | number of copies |
| `--no-collate` | off | do not collate copies |
| `--pages` | all | page selection, e.g. `1-3,5` |
| `--paper` | `A4` | paper size: A4, A5, A6, A3, B4, B5, B6, Letter, Legal, Ledger, Executive, HalfLetter, FS, Folio, Foolscap, Env10, Monarch, EnvDL, EnvC5, EnvC6, 8K, 16K, Postcard, ReplyPostcard |
| `--type` | `plainrecycled` | paper type: plainrecycled, plain, plain90, recycled, color, preprinted, prepunched, thick, thick160, letterhead, bond, cardstock, labels, envelope, thin, thinner |
| `--tray` | `auto` | `auto`, `bypass` (or `manual`), `tray1`, `tray2` |
| `--quality` | `fast` | `fast`, `standard` or `fine` (halftone screen set) |
| `--gray-k` | off | print neutral colours with black toner only |
| `--landscape` | off | print landscape documents full page (see below) |
| `--assets DIR` | `/usr/share/ricoh-spc240` (or `$RICOH_ASSETS`) | directory with `screens/` and `ricoh-c240dn.icc` |

Duplex is refused for paper sizes and paper types the printer cannot duplex.
`encode` accepts the same flags, but `--pages` has no effect there, since it
takes ready-made page rasters.

### Landscape documents

A landscape page is fed through the printer in portrait. With `--landscape`,
`print` renders every page on landscape media (images and text are laid out
landscape too), rotates it so that it fills the sheet (JPEG, PDF and text all follow the same convention). The top of
the content ends up on the right-hand long edge when the sheet is held in
portrait: turn the sheet 90 degrees counter-clockwise to read it. Without the
flag a landscape document is shrunk into the middle of the portrait sheet.
Pages that are already portrait print unchanged. Binding: a notepad-style landscape
document (bound along the landscape page's top, which is a long edge of the
sheet) needs `--duplex long`; a book-style one (bound along the landscape short
edge) needs `--duplex short`. `encode` and `testpage` reject `--landscape`
(pages are already rasters / fixed layout).

### Flags of `decode`

| Flag | Default | Meaning |
|---|---|---|
| `--png DIR` | | also write `page-NN.png` previews into DIR |

## Examples

```sh
alias ricoh='docker run --rm -v "$PWD":/data ghcr.io/kipavy/ricoh-spc240'

ricoh print /data/report.pdf --printer 192.168.1.23 --duplex long
ricoh print /data/report.pdf --printer 192.168.1.23 --mono --pages 1-3,5 --copies 2
ricoh print /data/notes.txt  --printer 192.168.1.23
ricoh print /data/photo.jpg  --printer 192.168.1.23 --quality fine

# Write the job to a file instead of printing, then inspect it
ricoh print /data/report.pdf --output /data/report.ddst
ricoh decode /data/report.ddst --png /data/preview
```

Pass `-e RICOH_PRINTER=192.168.1.23` to `docker run` to omit `--printer`.
Accepted inputs: PDF, PostScript, UTF-8 text, and JPEG, PNG, GIF, TIFF, BMP.

## Test sheet

```sh
ricoh testpage --printer 192.168.1.23
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
