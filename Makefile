# axscope installation.
#
# `make install` drops the binary into ~/.local/bin under its full name, so
# neither the shell nor opencode depends on a path inside the source tree.

PREFIX ?= $(HOME)/.local/bin
NAME    = axscope

.PHONY: build install uninstall vet

build:
	go build -o $(NAME) ./cmd/axscope

install:
	go build -o $(PREFIX)/$(NAME) ./cmd/axscope
	@echo "installed: $(PREFIX)/$(NAME)"

uninstall:
	rm -f $(PREFIX)/$(NAME)

vet:
	go vet ./...
