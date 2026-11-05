# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ricoh-spc240 ./cmd/ricoh-spc240

# Downloads Ricoh's halftone screens and ICC profile from support.ricoh.com.
FROM debian:bookworm-slim AS assets
RUN apt-get update \
 && apt-get install -y --no-install-recommends p7zip-full cpio curl ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY scripts/fetch-ricoh-assets.sh /usr/local/bin/fetch-ricoh-assets.sh
RUN bash /usr/local/bin/fetch-ricoh-assets.sh /usr/share/ricoh-spc240

FROM debian:bookworm-slim
LABEL org.opencontainers.image.source="https://github.com/kipavy/ricoh-spc240" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.description="Print to a Ricoh Aficio SP C240DN from Linux without the proprietary driver"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ghostscript img2pdf enscript \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ricoh-spc240 /usr/local/bin/ricoh-spc240
COPY --from=assets /usr/share/ricoh-spc240 /usr/share/ricoh-spc240
USER 65534:65534
WORKDIR /tmp
ENV HOME=/tmp
ENTRYPOINT ["ricoh-spc240"]
