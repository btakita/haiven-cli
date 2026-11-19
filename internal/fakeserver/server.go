package fakeserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Paths the server answers.
const (
	// WSPath is the gaming socket path the public WebSocket description
	// declares (servers.production.pathname).
	WSPath = "/v0/ws"
	// Subprotocol is the gaming socket subprotocol the AsyncAPI declares.
	// It is accepted when offered and never required.
	Subprotocol = "haiven.gaming.v0"
	// ControlPrefix holds the levers that replace wall-clock waits.
	ControlPrefix = "/control/"
)

// maxControlBody bounds a control request body.
const maxControlBody = 1 << 20

// outboxDepth bounds frames queued to one slow client; a client that falls
// further behind is disconnected rather than buffered without limit.
const outboxDepth = 4096

// Stats counts what the server saw. It is the sdk fake-server command's data.
type Stats struct {
	Connections     int `json:"connections" jsonschema_description:"Connections accepted."`
	Open            int `json:"open" jsonschema_description:"Connections still open."`
	ClientFrames    int `json:"client_frames"`
	ServerFrames    int `json:"server_frames" jsonschema_description:"Frames written to clients."`
	DroppedInbound  int `json:"dropped_inbound" jsonschema_description:"Client frames refused for an expired session."`
	DroppedOutbound int `json:"dropped_outbound" jsonschema_description:"Server frames the pump discarded for an expired session."`
	PolicyCloses    int `json:"policy_closes" jsonschema_description:"Handshakes refused with close code 1008."`
}

// Server serves the model over real sockets. Every model call happens under
// one lock, so a multi-client session is one deterministic transcript.
type Server struct {
	opts Options
	rec  *Recorder

	mu     sync.Mutex
	nextID int
	conns  map[int]*conn
	stats  Stats

	http     *http.Server
	listener net.Listener
	// handlers tracks open socket handlers, so Serve returns only after
	// every socket's last transcript entry is recorded.
	handlers sync.WaitGroup
}

// New returns a server; rec may be nil.
func New(opts Options, rec *Recorder) *Server {
	s := &Server{opts: opts, rec: rec, conns: map[int]*conn{}}
	mux := http.NewServeMux()
	mux.HandleFunc(WSPath, s.handleWS)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET "+ControlPrefix+"stats", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, s.Stats()) })
	mux.HandleFunc("POST "+ControlPrefix+"push", s.handlePush)
	mux.HandleFunc("POST "+ControlPrefix+"expire", s.each(func(sock *Socket) { sock.ExpireSession() }))
	mux.HandleFunc("POST "+ControlPrefix+"answer-deferred", s.each(func(sock *Socket) { sock.AnswerDeferred() }))
	mux.HandleFunc("POST "+ControlPrefix+"report-dropped", s.handleReportDropped)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return s
}

// Listen binds addr and returns the base http:// URL.
func (s *Server) Listen(addr string) (string, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	s.listener = l
	return "http://" + l.Addr().String(), nil
}

// Serve runs until ctx is done, then closes every socket normally and shuts
// the HTTP server down.
func (s *Server) Serve(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() { errc <- s.http.Serve(s.listener) }()
	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	s.mu.Lock()
	for _, c := range s.sortedConns() {
		c.sock.CloseNormally()
	}
	s.mu.Unlock()
	// Give clients a moment to answer the close handshake, then drop the
	// ones that do not, so a stuck client cannot hold the process open.
	if !s.waitHandlers(time.Second) {
		s.mu.Lock()
		for _, c := range s.sortedConns() {
			_ = c.ws.CloseNow()
		}
		s.mu.Unlock()
		s.waitHandlers(time.Second)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.http.Shutdown(shutdown)
	if errors.Is(err, context.DeadlineExceeded) {
		err = s.http.Close()
	}
	return err
}

// waitHandlers waits up to d for every socket handler to return.
func (s *Server) waitHandlers(d time.Duration) bool {
	waited := make(chan struct{})
	go func() {
		s.handlers.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		return true
	case <-time.After(d):
		return false
	}
}

// Stats returns a snapshot of the counters.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stats
	st.Open = len(s.conns)
	return st
}

func (s *Server) sortedConns() []*conn {
	ids := make([]int, 0, len(s.conns))
	for id := range s.conns {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	out := make([]*conn, len(ids))
	for i, id := range ids {
		out[i] = s.conns[id]
	}
	return out
}

// conn is one accepted socket. It implements Peer and Observer; both are
// called with s.mu held.
type conn struct {
	s    *Server
	ws   *websocket.Conn
	id   int
	sock *Socket
	out  chan outMsg
	full bool
}

type outMsg struct {
	data   []byte
	close  bool
	code   int
	reason string
}

func (c *conn) enqueue(m outMsg) {
	if c.full {
		return
	}
	select {
	case c.out <- m:
	default:
		c.full = true
		close(c.out)
	}
}

func (c *conn) Deliver(f ServerFrame) {
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	c.s.stats.ServerFrames++
	c.s.rec.Record(Entry{Conn: c.id, Event: EventServer, Frame: data})
	c.enqueue(outMsg{data: data})
}

func (c *conn) Close(code int, reason string) {
	if code == StatusPolicyViolation {
		c.s.stats.PolicyCloses++
	}
	c.s.rec.Record(Entry{Conn: c.id, Event: EventClose, Code: code, Reason: reason})
	c.enqueue(outMsg{close: true, code: code, reason: reason})
}

func (c *conn) DroppedInbound(f ClientFrame) {
	c.s.stats.DroppedInbound++
	data, _ := json.Marshal(f)
	c.s.rec.Record(Entry{Conn: c.id, Event: EventDroppedInbound, Frame: data})
}

func (c *conn) DroppedOutbound(f ServerFrame) {
	c.s.stats.DroppedOutbound++
	data, _ := json.Marshal(f)
	c.s.rec.Record(Entry{Conn: c.id, Event: EventDroppedOutbound, Frame: data})
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{Subprotocol},
		// A local fake for engine editors and WebGL builds served from any
		// origin; it binds loopback by default.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	s.handlers.Add(1)
	defer s.handlers.Done()
	ws.SetReadLimit(1 << 20)

	s.mu.Lock()
	s.nextID++
	c := &conn{s: s, ws: ws, id: s.nextID, out: make(chan outMsg, outboxDepth)}
	c.sock = NewSocket(s.opts, c, c)
	s.conns[c.id] = c
	s.stats.Connections++
	s.rec.Record(Entry{Conn: c.id, Event: EventOpen})
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.write(ctx, ws)
	}()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		s.mu.Lock()
		s.stats.ClientFrames++
		s.rec.Record(Entry{Conn: c.id, Event: EventClient, Frame: rawOrString(data)})
		c.sock.Receive(data)
		s.mu.Unlock()
	}

	s.mu.Lock()
	delete(s.conns, c.id)
	if !c.sock.Closed() {
		s.rec.Record(Entry{Conn: c.id, Event: EventDisconnect})
	}
	if !c.full {
		c.full = true
		close(c.out)
	}
	s.mu.Unlock()
	cancel()
	<-done
	_ = ws.CloseNow()
}

// write drains the outbox onto the socket in order.
func (c *conn) write(ctx context.Context, ws *websocket.Conn) {
	for m := range c.out {
		if m.close {
			_ = ws.Close(websocket.StatusCode(m.code), m.reason)
			return
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := ws.Write(wctx, websocket.MessageText, m.data)
		cancel()
		if err != nil {
			_ = ws.CloseNow()
			return
		}
	}
	// The outbox overflowed or the reader ended.
	_ = ws.CloseNow()
}

// targets selects authenticated sockets, of one user when ?user= is set.
func (s *Server) targets(r *http.Request) []*conn {
	user := r.URL.Query().Get("user")
	var out []*conn
	for _, c := range s.sortedConns() {
		if c.sock.UserID() == "" || c.sock.Closed() {
			continue
		}
		if user != "" && c.sock.UserID() != user {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ControlResult is every control endpoint's answer.
type ControlResult struct {
	Sockets int `json:"sockets" jsonschema_description:"Authenticated sockets the lever was applied to."`
}

func (s *Server) each(fn func(*Socket)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ts := s.targets(r)
		for _, c := range ts {
			fn(c.sock)
		}
		s.mu.Unlock()
		writeJSON(w, ControlResult{Sockets: len(ts)})
	}
}

// handlePush fans out one frame, or an array of frames as one burst, to the
// selected sockets: the shape a downstream service publishes to user:<id>.
func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxControlBody))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	frames, err := decodeFrames(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.each(func(sock *Socket) { sock.Push(frames...) })(w, r)
}

func decodeFrames(body []byte) ([]ServerFrame, error) {
	var many []ServerFrame
	if err := json.Unmarshal(body, &many); err == nil {
		for _, f := range many {
			if f.Type == "" {
				return nil, errors.New("every frame needs a type")
			}
		}
		return many, nil
	}
	var one ServerFrame
	if err := json.Unmarshal(body, &one); err != nil {
		return nil, fmt.Errorf("body is not a frame or an array of frames: %w", err)
	}
	if one.Type == "" {
		return nil, errors.New("the frame needs a type")
	}
	return []ServerFrame{one}, nil
}

func (s *Server) handleReportDropped(w http.ResponseWriter, r *http.Request) {
	var report DroppedFrames
	if err := json.NewDecoder(io.LimitReader(r.Body, maxControlBody)).Decode(&report); err != nil {
		http.Error(w, "body is not a dropped-frames report: "+err.Error(), http.StatusBadRequest)
		return
	}
	if report.Types == nil {
		report.Types = []TypeCount{}
	}
	if report.RequestIDs == nil {
		report.RequestIDs = []string{}
	}
	s.each(func(sock *Socket) { sock.ReportDropped(report) })(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// rawOrString keeps a JSON client frame as-is and quotes anything else, so
// the transcript is always valid JSON.
func rawOrString(data []byte) json.RawMessage {
	if json.Valid(data) {
		return append(json.RawMessage(nil), data...)
	}
	quoted, _ := json.Marshal(string(data))
	return quoted
}
