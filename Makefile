BINARY  := tmux-sidebar
VERSION ?= dev

.PHONY: build install reinstall

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

install:
	go install -ldflags "-X main.version=$(VERSION)" ./...
	cp $(shell go env GOPATH)/bin/$(BINARY) $(HOME)/.local/bin/$(BINARY)
	@echo "Installed to $(HOME)/.local/bin/$(BINARY) and $(shell go env GOPATH)/bin/$(BINARY)"

reinstall: install
	$(BINARY) restart
