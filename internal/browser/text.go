// Helpers de texto da leitura: normalizar, cortar e reconhecer o que não é
// conteúdo.
package browser

import (
	"strings"
	"unicode"
)

func repeatOf(text, parent string) bool {
	if text == "" || parent == "" {
		return false
	}
	return strings.Contains(strings.ToLower(parent), strings.ToLower(text))
}

// separatorOnly diz se o texto é só pontuação de layout ("|", "·", "•") — não
// é conteúdo, é separador desenhado com texto.

func separatorOnly(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("|·•/–—»«›<>:;,.()[]{}\u00a0", r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func norm(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
