// Package fakeserver is `haiven sdk fake-server`: a local model of the deployed
// Haiven gaming WebSocket, served over a real socket so engine editors and
// multi-client Play sessions can run without a backend.
//
// The model is a port of the TypeScript SDK's src/ws/fake-server.ts (the
// FakeServerSocket the JS binding's tests drive), which models the backend's
// read loop and write pump. Where the model and the backend disagree, the
// backend is right and the model is a bug: re-derive it from the live suite,
// never by adjusting it until a client passes.
package fakeserver

import (
	"encoding/json"
)

// Frame types the model itself writes or reads.
const (
	TypeAuth                = "auth"
	TypeAuthSuccess         = "auth.success"
	TypeAuthFailure         = "auth.failure"
	TypeAuthExpired         = "auth.expired"
	TypeAuthExpiredDropped  = "auth.expired.dropped"
	TypePing                = "ping"
	TypePong                = "pong"
	ErrExpiredToken         = "expired_token"
	ErrInvalidPayload       = "invalid_payload"
	ErrInvalidToken         = "invalid_token"
	ErrUnauthorized         = "unauthorized"
	ErrRateLimited          = "rate_limited"
	ErrUnknownMessageType   = "unknown_message_type"
	StatusPolicyViolation   = 1008
	StatusNormalClosure     = 1000
	closeReasonUnauthorized = "unauthorized"
)

// ClientFrame is one client -> server frame.
type ClientFrame struct {
	Type      string          `json:"type"`
	RequestID string          `json:"requestId"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// ServerFrame is one server -> client frame. RequestID and Success are
// pointers because a fan-out event carries neither, while a reply always
// carries both (an empty requestId included).
type ServerFrame struct {
	Type      string          `json:"type"`
	RequestID *string         `json:"requestId,omitempty"`
	Success   *bool           `json:"success,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// reply builds a correlated reply frame.
func reply(typ, requestID string, success bool, errCode string) ServerFrame {
	return ServerFrame{Type: typ, RequestID: &requestID, Success: &success, Error: errCode}
}

// DroppedFrames is the payload of auth.expired.dropped.
// Truncated is always serialized. Clients blind-resend only when it is
// true, so an omitted false is not a complete report.
type DroppedFrames struct {
	Count      int         `json:"count"`
	Types      []TypeCount `json:"types"`
	RequestIDs []string    `json:"requestIds"`
	Truncated  bool        `json:"truncated"`
}

// TypeCount is one row of DroppedFrames.Types.
type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// Token is one bearer token the fake verifier knows.
type Token struct {
	Value  string `json:"value"`
	UserID string `json:"user_id"`
	// Valid is whether the verifier accepts it at all.
	Valid bool `json:"valid"`
	// Expired is whether the session it mints is already past its expiry.
	Expired bool `json:"expired"`
}

// Options configures the model. It is shared by every socket of a server.
type Options struct {
	Tokens []Token
	// RateLimited lists frame types the per-route token bucket rejects.
	RateLimited []string
	// Routes lists message types the router has a handler for. ping is
	// always routed (websocket.NewRouter mounts it).
	Routes []string
	// Deferred lists routed types whose handler answers only when
	// AnswerDeferred runs, which is how a reply can meet a session that
	// lapsed after the request was accepted.
	Deferred []string
}

func set(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// Peer receives what the model writes: frames in order, then at most one
// close.
type Peer interface {
	Deliver(f ServerFrame)
	Close(code int, reason string)
}

// Observer sees the model's internal losses, for the transcript. Either
// method may be nil-safe no-ops.
type Observer interface {
	DroppedInbound(f ClientFrame)
	DroppedOutbound(f ServerFrame)
}

type session struct {
	userID  string
	expired bool
}

// Socket is the server side of one connection. It is not safe for
// concurrent use; the server serializes calls per socket.
type Socket struct {
	opts        Options
	routes      map[string]bool
	rateLimited map[string]bool
	deferred    map[string]bool
	peer        Peer
	obs         Observer

	session *session
	closed  bool

	// outbox holds frames the pump resolved but has not written: the read
	// loop finishes handling a client frame before any of what it wrote
	// reaches the socket.
	outbox    []ServerFrame
	stepDepth int
	draining  bool
	held      []ClientFrame

	// Sent, DroppedInbound and DroppedOutbound mirror the JS model's
	// transcript fields.
	Sent            []ClientFrame
	DroppedInbound  []ClientFrame
	DroppedOutbound []ServerFrame
}

// NewSocket returns the server side of a new connection.
func NewSocket(opts Options, peer Peer, obs Observer) *Socket {
	routes := set(opts.Routes)
	routes[TypePing] = true
	return &Socket{
		opts:        opts,
		routes:      routes,
		rateLimited: set(opts.RateLimited),
		deferred:    set(opts.Deferred),
		peer:        peer,
		obs:         obs,
	}
}

// UserID is the authenticated user, or "" before the handshake.
func (s *Socket) UserID() string {
	if s.session == nil {
		return ""
	}
	return s.session.userID
}

// Closed reports whether the server closed the socket.
func (s *Socket) Closed() bool { return s.closed }

// Receive handles one client frame. raw is the text message; a frame that is
// not JSON is handled like a frame with no usable fields.
func (s *Socket) Receive(raw []byte) {
	if s.closed {
		return
	}
	var f ClientFrame
	_ = json.Unmarshal(raw, &f)
	s.Sent = append(s.Sent, f)
	s.step(func() {
		if s.session == nil {
			s.handshake(f)
			return
		}
		s.readLoop(f)
	})
}

func (s *Socket) step(fn func()) {
	s.stepDepth++
	defer func() {
		s.stepDepth--
		if s.stepDepth == 0 {
			s.drain()
		}
	}()
	fn()
}

func (s *Socket) drain() {
	if s.draining {
		return
	}
	s.draining = true
	defer func() { s.draining = false }()
	for len(s.outbox) > 0 {
		f := s.outbox[0]
		s.outbox = s.outbox[1:]
		if s.closed {
			return
		}
		s.peer.Deliver(f)
	}
}

func (s *Socket) close(code int, reason string) {
	if s.closed {
		return
	}
	s.closed = true
	s.peer.Close(code, reason)
}

// handshake is readLoop -> initAuth -> promoteConn. Every failure closes the
// socket with 1008 and no auth.failure frame, and there is no expiry check:
// a token past its exp but inside the verifier leeway authenticates into a
// session that is expired from its first frame.
func (s *Socket) handshake(f ClientFrame) {
	tok, ok := s.resolve(f)
	if f.Type != TypeAuth || !ok || !tok.Valid {
		s.close(StatusPolicyViolation, closeReasonUnauthorized)
		return
	}
	s.session = &session{userID: tok.UserID, expired: tok.Expired}
	// requestId is hard-coded to "", discarding the client's.
	if s.session.expired {
		s.writePump(reply(TypeAuthExpired, "", false, ErrExpiredToken))
	} else {
		s.writePump(reply(TypeAuthSuccess, "", true, ""))
	}
}

func (s *Socket) readLoop(f ClientFrame) {
	sess := s.session
	// The rate-limit gate runs before the expiry gate and answers with the
	// client's own frame type.
	if s.rateLimited[f.Type] {
		s.writePump(reply(f.Type, f.RequestID, false, ErrRateLimited))
		return
	}
	// Expiry is evaluated lazily, on the client's next frame.
	if sess.expired || f.Type == TypeAuth {
		if f.Type == TypeAuth {
			s.reauth(f, sess)
			return
		}
		s.DroppedInbound = append(s.DroppedInbound, f)
		if s.obs != nil {
			s.obs.DroppedInbound(f)
		}
		s.writePump(reply(TypeAuthExpired, f.RequestID, false, ErrExpiredToken))
		return
	}
	s.dispatch(f)
}

func (s *Socket) dispatch(f ClientFrame) {
	if !s.routes[f.Type] {
		s.writePump(reply(f.Type, f.RequestID, false, ErrUnknownMessageType))
		return
	}
	if s.deferred[f.Type] {
		s.held = append(s.held, f)
		return
	}
	if f.Type == TypePing {
		s.writePump(reply(TypePong, f.RequestID, true, ""))
		return
	}
	s.writePump(reply(f.Type, f.RequestID, true, ""))
}

func (s *Socket) reauth(f ClientFrame, sess *session) {
	tok, ok := s.resolve(f)
	switch {
	case !ok:
		s.writePump(reply(TypeAuthFailure, f.RequestID, false, ErrInvalidPayload))
		return
	case !tok.Valid:
		s.writePump(reply(TypeAuthFailure, f.RequestID, false, ErrInvalidToken))
		return
	case tok.UserID != sess.userID:
		s.writePump(reply(TypeAuthFailure, f.RequestID, false, ErrUnauthorized))
		return
	}
	// The session is replaced before the reply is written, so the reply
	// reports the session the client now has.
	sess.userID = tok.UserID
	sess.expired = tok.Expired
	if sess.expired {
		s.writePump(reply(TypeAuthExpired, f.RequestID, false, ErrExpiredToken))
	} else {
		s.writePump(reply(TypeAuthSuccess, f.RequestID, true, ""))
	}
}

// Push is a server-originated fan-out the client did not ask for.
func (s *Socket) Push(frames ...ServerFrame) {
	s.step(func() {
		for _, f := range frames {
			s.writePump(f)
		}
	})
}

// writePump is the single exit from the server. While the session is
// expired, content is replaced by an uncorrelated auth.expired and
// discarded; auth-control frames pass through.
func (s *Socket) writePump(f ServerFrame) {
	if s.session != nil && s.session.expired && !isAuthControl(f) {
		s.DroppedOutbound = append(s.DroppedOutbound, f)
		if s.obs != nil {
			s.obs.DroppedOutbound(f)
		}
		s.deliver(reply(TypeAuthExpired, "", false, ErrExpiredToken))
		return
	}
	s.deliver(f)
}

// AnswerDeferred runs every held handler, oldest request first.
func (s *Socket) AnswerDeferred() {
	s.step(func() {
		held := s.held
		s.held = nil
		for _, f := range held {
			s.writePump(reply(f.Type, f.RequestID, true, ""))
		}
	})
}

// ReportDropped writes the loss report a parking backend sends at the head
// of its replay. The modelled pump discards rather than parks, so this is a
// control lever, not modelled behaviour.
func (s *Socket) ReportDropped(report DroppedFrames) {
	payload, _ := json.Marshal(report)
	f := reply(TypeAuthExpiredDropped, "", false, ErrExpiredToken)
	f.Payload = payload
	s.step(func() { s.writePump(f) })
}

// ExpireSession expires the session without a wall-clock wait.
func (s *Socket) ExpireSession() {
	if s.session != nil {
		s.session.expired = true
	}
}

// CloseNormally closes the socket from the server side (shutdown).
func (s *Socket) CloseNormally() { s.close(StatusNormalClosure, "server shutting down") }

func (s *Socket) resolve(f ClientFrame) (Token, bool) {
	var value string
	if len(f.Payload) == 0 || json.Unmarshal(f.Payload, &value) != nil {
		return Token{}, false
	}
	for _, t := range s.opts.Tokens {
		if t.Value == value {
			return t, true
		}
	}
	return Token{}, false
}

func (s *Socket) deliver(f ServerFrame) {
	if s.closed {
		return
	}
	s.outbox = append(s.outbox, f)
}

func isAuthControl(f ServerFrame) bool {
	switch f.Type {
	case TypeAuthSuccess, TypeAuthFailure, TypeAuthExpired, TypeAuthExpiredDropped:
		return true
	}
	return false
}
