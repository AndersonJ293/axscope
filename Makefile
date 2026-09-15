# Instalação do browser-use.
#
# `make install` põe o binário em ~/.local/bin com o nome completo e cria o
# atalho `bu`. Assim nem o shell nem o opencode dependem de caminho dentro da
# árvore do código-fonte.

PREFIX ?= $(HOME)/.local/bin
NAME    = browser-use

.PHONY: build install uninstall vet

build:
	go build -o bu ./cmd/bu

install:
	go build -o $(PREFIX)/$(NAME) ./cmd/bu
	ln -sf $(NAME) $(PREFIX)/bu
	@echo "instalado: $(PREFIX)/$(NAME) (atalho: bu)"

uninstall:
	rm -f $(PREFIX)/$(NAME) $(PREFIX)/bu

vet:
	go vet ./...
