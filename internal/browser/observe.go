// Captura passiva de console, rede e diálogos, por sessão (aba).
//
// Fica em buffers circulares: o agente lê sob demanda com `console`/`net`, e os
// erros ficam disponíveis mesmo depois de o evento ter passado.
package browser

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/ajunior/browser-use/internal/cdp"
)

// ConsoleEntry é uma linha de console ou uma exceção da página.
type ConsoleEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Text    string    `json:"text"`
	URL     string    `json:"url,omitempty"`
	Line    int       `json:"line,omitempty"`
}

// NetworkEntry é uma requisição observada.
type NetworkEntry struct {
	Time     time.Time `json:"time"`
	Method   string    `json:"method"`
	URL      string    `json:"url"`
	Resource string    `json:"resource,omitempty"`
	Status   int       `json:"status,omitempty"`
	Failed   string    `json:"failed,omitempty"`
}

// DialogEntry é um diálogo nativo (alert/confirm/prompt/beforeunload).
type DialogEntry struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"`
	Message string    `json:"message"`
	Handled string    `json:"handled"`
}

type Observe struct {
	mu      sync.Mutex
	max     int
	console map[string][]ConsoleEntry
	network map[string][]NetworkEntry
	dialogs map[string][]DialogEntry
}

func NewObserve(max int) *Observe {
	if max <= 0 {
		max = 500
	}
	return &Observe{
		max:     max,
		console: make(map[string][]ConsoleEntry),
		network: make(map[string][]NetworkEntry),
		dialogs: make(map[string][]DialogEntry),
	}
}

func appendCapped[T any](buf []T, item T, max int) []T {
	buf = append(buf, item)
	if len(buf) > max {
		buf = buf[len(buf)-max:]
	}
	return buf
}

// Console devolve as entradas de uma sessão, opcionalmente só de um nível.
func (o *Observe) Console(session, level string, limit int) []ConsoleEntry {
	o.mu.Lock()
	defer o.mu.Unlock()
	all := o.console[session]
	out := make([]ConsoleEntry, 0, len(all))
	for _, e := range all {
		if level != "" && level != "all" && e.Level != level {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Network devolve requisições, opcionalmente filtrando por substring de URL.
func (o *Observe) Network(session, filter string, limit int) []NetworkEntry {
	o.mu.Lock()
	defer o.mu.Unlock()
	all := o.network[session]
	out := make([]NetworkEntry, 0, len(all))
	for _, e := range all {
		if filter != "" && !strings.Contains(e.URL, filter) {
			continue
		}
		out = append(out, e)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// Dialogs devolve os diálogos registrados numa sessão.
func (o *Observe) Dialogs(session string) []DialogEntry {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]DialogEntry(nil), o.dialogs[session]...)
}

// ClearNetwork zera o buffer de rede de uma sessão (útil antes de um passo).
func (o *Observe) ClearNetwork(session string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.network, session)
}

func (o *Observe) addConsole(session string, e ConsoleEntry) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.console[session] = appendCapped(o.console[session], e, o.max)
}

func (o *Observe) addNetwork(session string, e NetworkEntry) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.network[session] = appendCapped(o.network[session], e, o.max)
}

func (o *Observe) addDialog(session string, e DialogEntry) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.dialogs[session] = appendCapped(o.dialogs[session], e, o.max)
}

// Wire registra os handlers de console, rede e exceções (uma vez por conexão).
func (o *Observe) Wire(c *cdp.Client) {
	c.On("Runtime.consoleAPICalled", func(params json.RawMessage, sid string) {
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Value       any    `json:"value"`
				Description string `json:"description"`
				Type        string `json:"type"`
			} `json:"args"`
			StackTrace struct {
				CallFrames []struct {
					URL    string `json:"url"`
					LineNo int    `json:"lineNumber"`
				} `json:"callFrames"`
			} `json:"stackTrace"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		parts := make([]string, 0, len(p.Args))
		for _, a := range p.Args {
			parts = append(parts, remoteObjectText(a.Value, a.Description, a.Type))
		}
		entry := ConsoleEntry{
			Time:  time.Now(),
			Level: consoleLevel(p.Type),
			Text:  strings.Join(parts, " "),
		}
		if len(p.StackTrace.CallFrames) > 0 {
			entry.URL = p.StackTrace.CallFrames[0].URL
			entry.Line = p.StackTrace.CallFrames[0].LineNo
		}
		o.addConsole(sid, entry)
	})

	c.On("Runtime.exceptionThrown", func(params json.RawMessage, sid string) {
		var p struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				URL       string `json:"url"`
				LineNo    int    `json:"lineNumber"`
				Exception *struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		text := p.ExceptionDetails.Text
		if p.ExceptionDetails.Exception != nil && p.ExceptionDetails.Exception.Description != "" {
			text = p.ExceptionDetails.Exception.Description
		}
		o.addConsole(sid, ConsoleEntry{
			Time:  time.Now(),
			Level: "error",
			Text:  text,
			URL:   p.ExceptionDetails.URL,
			Line:  p.ExceptionDetails.LineNo,
		})
	})

	c.On("Network.requestWillBeSent", func(params json.RawMessage, sid string) {
		var p struct {
			Request struct {
				Method string `json:"method"`
				URL    string `json:"url"`
			} `json:"request"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		o.addNetwork(sid, NetworkEntry{
			Time:     time.Now(),
			Method:   p.Request.Method,
			URL:      p.Request.URL,
			Resource: p.Type,
		})
	})

	c.On("Network.responseReceived", func(params json.RawMessage, sid string) {
		var p struct {
			Response struct {
				URL    string `json:"url"`
				Status int    `json:"status"`
			} `json:"response"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		o.mu.Lock()
		defer o.mu.Unlock()
		buf := o.network[sid]
		for i := len(buf) - 1; i >= 0; i-- {
			if buf[i].URL == p.Response.URL && buf[i].Status == 0 {
				buf[i].Status = p.Response.Status
				break
			}
		}
	})

	c.On("Network.loadingFailed", func(params json.RawMessage, sid string) {
		var p struct {
			RequestID string `json:"requestId"`
			ErrorText string `json:"errorText"`
			Type      string `json:"type"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		o.addNetwork(sid, NetworkEntry{
			Time:     time.Now(),
			Failed:   p.ErrorText,
			Resource: p.Type,
		})
	})
}

func remoteObjectText(value any, description, typ string) string {
	if value != nil {
		if s, ok := value.(string); ok {
			return s
		}
		b, err := json.Marshal(value)
		if err == nil {
			return string(b)
		}
	}
	if description != "" {
		return description
	}
	if typ != "" {
		return "<" + typ + ">"
	}
	return ""
}

func consoleLevel(t string) string {
	switch t {
	case "error", "assert":
		return "error"
	case "warning":
		return "warn"
	case "debug", "verbose":
		return "debug"
	case "info":
		return "info"
	default:
		return "log"
	}
}
