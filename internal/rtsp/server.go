package rtsp

import (
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"

	"mockcam/internal/auth"
	"mockcam/internal/config"
)

// Server handles RTSP streaming and fanout.
type Server struct {
	cfgMgr       *config.Manager
	auth         *auth.Authenticator
	rtspServer   *gortsplib.Server
	streams      map[string]*gortsplib.ServerStream
	mu           sync.RWMutex
	clientCount  atomic.Int64
	sessionPaths map[*gortsplib.ServerSession]string
}

// NewServer creates a new RTSP server.
func NewServer(cfgMgr *config.Manager, authenticator *auth.Authenticator) *Server {
	s := &Server{
		cfgMgr:       cfgMgr,
		auth:         authenticator,
		streams:      make(map[string]*gortsplib.ServerStream),
		sessionPaths: make(map[*gortsplib.ServerSession]string),
	}
	return s
}

// Start launches the RTSP server.
func (s *Server) Start() error {
	cfg := s.cfgMgr.Get()
	port := cfg.Server.RTSPPort
	if port <= 0 {
		port = 8554
	}

	s.rtspServer = &gortsplib.Server{
		Handler:        s,
		RTSPAddress:    fmt.Sprintf(":%d", port),
		WriteQueueSize: 4096,
	}

	log.Printf("[rtsp] Starting RTSP server on :%d...", port)
	return s.rtspServer.Start()
}

// Close gracefully closes the RTSP server and streams.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, stream := range s.streams {
		stream.Close()
	}
	s.streams = make(map[string]*gortsplib.ServerStream)

	if s.rtspServer != nil {
		s.rtspServer.Close()
	}
	log.Println("[rtsp] RTSP server closed")
}

// GetClientCount returns the number of active RTSP client sessions.
func (s *Server) GetClientCount() int64 {
	return s.clientCount.Load()
}

func normalizePath(p string) string {
	return strings.TrimPrefix(strings.TrimPrefix(p, "/"), "live/")
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Server) checkAuth(req *base.Request, isPublish bool, remoteAddr string) *base.Response {
	// Allow unauthenticated publish from loopback (internal FFmpeg)
	if isPublish && isLoopback(remoteAddr) {
		return nil
	}

	cfg := s.cfgMgr.Get()
	if !auth.IsAuthEnabled(cfg.Server.AuthType) {
		return nil
	}

	authHeaders := req.Header["Authorization"]
	if len(authHeaders) == 0 {
		return s.makeUnauthorizedResponse()
	}

	authHeader := authHeaders[0]
	if strings.HasPrefix(strings.ToLower(authHeader), "basic ") {
		payload := strings.TrimSpace(authHeader[6:])
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil || !s.auth.ValidateBasicCredentials(strings.Split(string(decoded), ":")[0], strings.Split(string(decoded), ":")[1]) {
			return s.makeUnauthorizedResponse()
		}
		return nil
	}

	if strings.HasPrefix(strings.ToLower(authHeader), "digest ") {
		params := auth.ParseDigestAuthorization(authHeader)
		if !s.auth.DigestManager().ValidateDigest(string(req.Method), params, cfg.Server.AuthUser, cfg.Server.AuthPass) {
			return s.makeUnauthorizedResponse()
		}
		return nil
	}

	return s.makeUnauthorizedResponse()
}

func (s *Server) makeUnauthorizedResponse() *base.Response {
	cfg := s.cfgMgr.Get()
	var authVal string
	if cfg.Server.AuthType == "basic" {
		authVal = fmt.Sprintf(`Basic realm="%s"`, s.auth.Realm())
	} else {
		authVal = s.auth.DigestManager().ChallengeHeader()
	}

	return &base.Response{
		StatusCode: base.StatusUnauthorized,
		Header: base.Header{
			"WWW-Authenticate": base.HeaderValue{authVal},
		},
	}
}

// OnConnOpen is called when a client TCP connection is opened.
func (s *Server) OnConnOpen(ctx *gortsplib.ServerHandlerOnConnOpenCtx) {
	s.clientCount.Add(1)
}

// OnConnClose is called when a client TCP connection is closed.
func (s *Server) OnConnClose(ctx *gortsplib.ServerHandlerOnConnCloseCtx) {
	s.clientCount.Add(-1)
}

// OnSessionOpen is called when a session is opened.
func (s *Server) OnSessionOpen(ctx *gortsplib.ServerHandlerOnSessionOpenCtx) {}

// OnSessionClose is called when a session is closed.
func (s *Server) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessionPaths, ctx.Session)
}

// OnDescribe is called when receiving a DESCRIBE request.
func (s *Server) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	if resp := s.checkAuth(ctx.Request, false, ctx.Conn.NetConn().RemoteAddr().String()); resp != nil {
		return resp, nil, nil
	}

	token := normalizePath(ctx.Path)
	s.mu.RLock()
	stream, exists := s.streams[token]
	s.mu.RUnlock()

	if !exists {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, stream, nil
}

// OnAnnounce is called when a publisher announces a stream.
func (s *Server) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (*base.Response, error) {
	if resp := s.checkAuth(ctx.Request, true, ctx.Conn.NetConn().RemoteAddr().String()); resp != nil {
		return resp, nil
	}

	token := normalizePath(ctx.Path)
	stream := gortsplib.NewServerStream(s.rtspServer, ctx.Description)

	s.mu.Lock()
	if old, exists := s.streams[token]; exists {
		old.Close()
	}
	s.streams[token] = stream
	s.sessionPaths[ctx.Session] = token
	s.mu.Unlock()

	log.Printf("[rtsp] Stream announced: token='%s'", token)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// OnSetup is called when a client sets up a track.
func (s *Server) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	token := normalizePath(ctx.Path)

	s.mu.RLock()
	stream, exists := s.streams[token]
	s.mu.RUnlock()

	// If publisher session, return OK without stream
	if ctx.Session.State() == gortsplib.ServerSessionStatePreRecord {
		return &base.Response{StatusCode: base.StatusOK}, nil, nil
	}

	if !exists {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}

	return &base.Response{StatusCode: base.StatusOK}, stream, nil
}

// OnPlay is called when a reader starts playing.
func (s *Server) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// OnRecord is called when a publisher starts publishing.
func (s *Server) OnRecord(ctx *gortsplib.ServerHandlerOnRecordCtx) (*base.Response, error) {
	s.mu.RLock()
	token := s.sessionPaths[ctx.Session]
	stream := s.streams[token]
	s.mu.RUnlock()

	if stream == nil {
		return &base.Response{StatusCode: base.StatusBadRequest}, nil
	}

	ctx.Session.OnPacketRTPAny(func(medi *description.Media, forma format.Format, pkt *rtp.Packet) {
		_ = stream.WritePacketRTP(medi, pkt)
	})

	ctx.Session.OnPacketRTCPAny(func(medi *description.Media, pkt rtcp.Packet) {
		_ = stream.WritePacketRTCP(medi, pkt)
	})

	log.Printf("[rtsp] Publishing started for token='%s'", token)
	return &base.Response{StatusCode: base.StatusOK}, nil
}
