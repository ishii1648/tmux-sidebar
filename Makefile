BINARY  := tmux-sidebar
VERSION ?= dev

.PHONY: build install

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

install:
	go build -ldflags "-X main.version=$(VERSION)" -o $(HOME)/.local/bin/$(BINARY) .
	@echo "Installed $(HOME)/.local/bin/$(BINARY)"
