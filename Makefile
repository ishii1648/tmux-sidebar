BINARY  := tmux-sidebar
DESTDIR := $(shell go env GOPATH)/bin
VERSION ?= dev

.PHONY: build install

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

install: build
	cp $(BINARY) $(DESTDIR)/$(BINARY)
	@echo "Installed $(DESTDIR)/$(BINARY)"
