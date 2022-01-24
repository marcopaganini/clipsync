# This file is part of clipsync (C)2022 by Marco Paganini
# Please see http://github.com/marcopaganini/clipsync for details.

.PHONY: arch clean install

bin := clips
bindir := /usr/local/bin
archdir := arch
src := $(wildcard *.go)
git_tag := $(shell git describe --always --tags)

# Default target
${bin}: Makefile ${src}
	CGO_ENABLED=0 go build -v -ldflags "-X main.BuildVersion=${git_tag}" -o "${bin}"

clean:
	rm -f "${bin}"
	rm -f "docs/${bin}.1"
	rm -rf "${archdir}"

install: ${bin}
	install -m 755 "${bin}" "${bindir}"

# Creates cross-compiled tarred versions (for releases).
arch: Makefile ${src}
	for ga in "linux/amd64" "linux/386" "linux/arm" "linux/arm64" "linux/mips" "linux/mipsle"; do \
	  export GOOS="$${ga%/*}"; \
	  export GOARCH="$${ga#*/}"; \
	  dst="./${archdir}/$${GOOS}-$${GOARCH}"; \
	  mkdir -p "$${dst}"; \
	  echo "=== Building $${GOOS}/$${GOARCH} ==="; \
	  go build -v -ldflags "-X main.Build=${git_tag}" -o "$${dst}/${bin}"; \
	  [ -s LICENSE ] && install -m 644 LICENSE "$${dst}"; \
	  [ -s README.md ] && install -m 644 README.md "$${dst}"; \
	  [ -s docs/${bin}.1 ] && install -m 644 docs/${bin}.1 "$${dst}"; \
	  tar -C "${archdir}" -zcvf "${archdir}/${bin}-$${GOOS}-$${GOARCH}.tar.gz" "$${dst##*/}"; \
	done
