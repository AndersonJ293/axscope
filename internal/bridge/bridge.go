// Ponte com a extensão: o daemon abre um WebSocket local e a extensão se
// conecta. Do ponto de vista do resto do driver, é a mesma conexão CDP de
// sempre — a extensão só sintetiza o domínio Target (abas) e repassa o resto
// para o chrome.debugger.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/ajunior/browser-use/internal/cdp"
)

// DefaultPort é a primeira porta da faixa onde o daemon espera a extensão.
const DefaultPort = 8787

// portSpan é quantas sessões simultâneas cabem, uma porta por sessão.
const portSpan = 16

// Server aceita conexões da extensão e as entrega como clientes CDP.
type Server struct {
	mu       sync.Mutex
	listener net.Listener
	http     *http.Server
	port     int
	session  string
	label    string
	last     *cdp.Client
	conns    chan *cdp.Client
}

// SetLabel troca o nome exibido (ex.: "Opencode") e reavisa a extensão, que
// renomeia o grupo de abas mantendo o número que já tinha.
func (s *Server) SetLabel(label string) {
	s.mu.Lock()
	if label == "" || s.label == label {
		s.mu.Unlock()
		return
	}
	s.label = label
	last := s.last
	s.mu.Unlock()
	if last != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = last.Notify(ctx, "__session", map[string]any{"session": s.session, "agent": label})
	}
}

func (s *Server) currentLabel() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.label
}

// Start sobe o servidor em 127.0.0.1 (nunca exposto para fora da máquina) e
// anuncia à extensão a qual sessão ela pertence — é o que define o grupo de abas.
//
// Cada sessão ocupa uma porta da faixa: assim vários agentes rodam ao mesmo
// tempo, cada um com seu grupo de abas, sem disputar porta.
func Start(session string, basePort int) (*Server, error) {
	if basePort <= 0 {
		basePort = DefaultPort
	}
	var ln net.Listener
	var lastErr error
	for offset := 0; offset < portSpan; offset++ {
		candidate, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", basePort+offset))
		if err == nil {
			ln = candidate
			break
		}
		lastErr = err
	}
	if ln == nil {
		return nil, fmt.Errorf(
			"nenhuma porta livre entre %d e %d para a extensão: %w",
			basePort, basePort+portSpan-1, lastErr)
	}
	s := &Server{
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
		session:  session,
		conns:    make(chan *cdp.Client, 4),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/cdp", func(w http.ResponseWriter, r *http.Request) {
		// Só escutamos em loopback; a origem da extensão é chrome-extension://,
		// que não casaria com a checagem padrão de Origin.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		// Handshake: diz à extensão qual sessão/grupo de abas é dela e como
		// exibir esse grupo (o nome do agente que está dirigindo).
		hello, _ := json.Marshal(map[string]any{
			"method": "__session",
			"params": map[string]any{"session": s.session, "agent": s.currentLabel()},
		})
		wctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		_ = conn.Write(wctx, websocket.MessageText, hello)
		cancel()

		client := cdp.FromConn(conn)
		s.mu.Lock()
		s.last = client
		s.mu.Unlock()
		select {
		case s.conns <- client:
		default:
			// Já tem uma conexão ativa; descarta a extra em vez de acumular.
			client.Close()
			return
		}
		<-client.Done()
	})

	s.http = &http.Server{Handler: mux}
	go func() { _ = s.http.Serve(ln) }()
	return s, nil
}

// Port devolve a porta efetiva (útil quando 0 foi pedido).
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Wait espera a extensão conectar e devolve a conexão CDP.
func (s *Server) Wait(ctx context.Context, timeout time.Duration) (*cdp.Client, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case c := <-s.conns:
		return c, nil
	case <-timer.C:
		return nil, fmt.Errorf(
			"a extensão browser-use não conectou na porta %d em %s.\n"+
				"Confira: (1) o Brave está aberto; (2) a extensão está carregada em brave://extensions;\n"+
				"(3) o ícone da extensão mostra 'conectado'. Se o Brave não está aberto, use --ver ou --leve",
			s.Port(), timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close derruba o servidor.
func (s *Server) Close() {
	if s.http != nil {
		_ = s.http.Close()
	}
}
