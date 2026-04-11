BINARY  := tmux-sidebar
VERSION ?= dev

.PHONY: build install

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

install:
	go install -ldflags "-X main.version=$(VERSION)" ./...
	@echo "Installed $(shell go env GOPATH)/bin/$(BINARY)"
