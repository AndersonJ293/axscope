// Ponte com a extensão: o daemon abre um WebSocket local e a extensão se
// conecta. Do ponto de vista do resto do driver, é a mesma conexão CDP de
// sempre — a extensão só sintetiza o domínio Target (abas) e repassa o resto
// para o chrome.debugger.
package bridge

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/ajunior/browser-use/internal/cdp"
)

// DefaultPort é onde o daemon espera a extensão.
const DefaultPort = 8787

// Server aceita conexões da extensão e as entrega como clientes CDP.
type Server struct {
	mu       sync.Mutex
	listener net.Listener
	http     *http.Server
	port     int
	conns    chan *cdp.Client
}

// Start sobe o servidor em 127.0.0.1 (nunca exposto para fora da máquina).
func Start(port int) (*Server, error) {
	if port <= 0 {
		port = DefaultPort
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("abrindo a porta %d para a extensão: %w", port, err)
	}
	s := &Server{
		listener: ln,
		port:     ln.Addr().(*net.TCPAddr).Port,
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
		client := cdp.FromConn(conn)
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
