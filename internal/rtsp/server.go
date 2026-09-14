// Package rtsp implements the RTSP server that receives streams published by
// the internal FFmpeg workers and fans them out to any number of readers.
package rtsp

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"

	"mockcam/internal/config"
	"mockcam/internal/logger"
)

// pathPrefix is the URL prefix under which every profile stream is exposed.
const pathPrefix = "live/"

type clientSessionRecord struct {
	id        string
	remoteIP  string
	path      string
	startTime time.Time
}

// Server handles RTSP streaming and fanout.
type Server struct {
	cfgMgr     *config.Manager
	auth       CredentialValidator
	rtspServer *gortsplib.Server
	meter      *throughputMeter
	now        func() time.Time

	mu          sync.RWMutex
	streams     map[string]*gortsplib.ServerStream
	publishers  map[*gortsplib.ServerSession]string
	readers     map[*gortsplib.ServerSession]*clientSessionRecord
	connCount   int64
	readerSeq   int64
	writeQueue  int
	rtspAddress string
}

// NewServer creates a new RTSP server.
func NewServer(cfgMgr *config.Manager, authenticator CredentialValidator) *Server {
	now := time.Now
	return &Server{
		cfgMgr:     cfgMgr,
		auth:       authenticator,
		meter:      newThroughputMeter(now()),
		now:        now,
		streams:    make(map[string]*gortsplib.ServerStream),
		publishers: make(map[*gortsplib.ServerSession]string),
		readers:    make(map[*gortsplib.ServerSession]*clientSessionRecord),
		writeQueue: 8192,
	}
}

// Start launches the RTSP server on the configured port.
func (s *Server) Start() error {
	cfg := s.cfgMgr.Get()
	port := cfg.Server.RTSPPort
	if port <= 0 {
		port = 8554
	}
	s.rtspAddress = fmt.Sprintf(":%d", port)

	s.rtspServer = &gortsplib.Server{
		Handler:                  s,
		RTSPAddress:              s.rtspAddress,
		WriteQueueSize:           s.writeQueue,
		DisableRTCPSenderReports: true,
	}

	log.Printf("[rtsp] Starting RTSP server on %s...", s.rtspAddress)
	return s.rtspServer.Start()
}

// Close gracefully closes the RTSP server and all streams.
func (s *Server) Close() {
	s.mu.Lock()
	for _, stream := range s.streams {
		stream.Close()
	}
	s.streams = make(map[string]*gortsplib.ServerStream)
	s.mu.Unlock()

	if s.rtspServer != nil {
		s.rtspServer.Close()
	}
	log.Println("[rtsp] RTSP server closed")
}

// GetClientCount returns the number of open RTSP TCP connections.
func (s *Server) GetClientCount() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connCount
}

// StreamTokens returns the tokens that currently have an active publisher.
func (s *Server) StreamTokens() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tokens := make([]string, 0, len(s.streams))
	for t := range s.streams {
		tokens = append(tokens, t)
	}
	return tokens
}

// normalizePath maps an RTSP request path ("/live/Profile_1") to a profile token.
func normalizePath(p string) string {
	return strings.TrimPrefix(strings.TrimPrefix(p, "/"), pathPrefix)
}

// isLoopback reports whether the remote address belongs to a loopback interface.
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func remoteAddrOf(conn *gortsplib.ServerConn) string {
	if conn == nil || conn.NetConn() == nil {
		return ""
	}
	return conn.NetConn().RemoteAddr().String()
}

func remoteHostOf(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

func (s *Server) checkAuth(req *base.Request, isPublish bool, remoteAddr string) *base.Response {
	cfg := s.cfgMgr.Get()
	return authorizeRequest(req, isPublish, remoteAddr, cfg.Server.AuthType, cfg.Server.AuthUser, cfg.Server.AuthPass, s.auth)
}

// OnConnOpen is called when a client TCP connection is opened.
func (s *Server) OnConnOpen(ctx *gortsplib.ServerHandlerOnConnOpenCtx) {
	s.mu.Lock()
	s.connCount++
	s.mu.Unlock()
}

// OnConnClose is called when a client TCP connection is closed.
func (s *Server) OnConnClose(ctx *gortsplib.ServerHandlerOnConnCloseCtx) {
	s.mu.Lock()
	s.connCount--
	s.mu.Unlock()
}

// OnSessionOpen is called when a session is opened.
func (s *Server) OnSessionOpen(ctx *gortsplib.ServerHandlerOnSessionOpenCtx) {}

// OnSessionClose is called when a session is closed. Reader sessions are
// removed from the client list; when a publisher disappears its stream is
// closed so that readers reconnect instead of waiting on a dead stream.
func (s *Server) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.readers[ctx.Session]; ok {
		logger.Infof("rtsp", "Client disconnected: %s from %s (viewed %s for %ds)",
			rec.id, rec.remoteIP, rec.path, int64(s.now().Sub(rec.startTime).Seconds()))
		delete(s.readers, ctx.Session)
	}

	if token, ok := s.publishers[ctx.Session]; ok {
		delete(s.publishers, ctx.Session)
		if stream, exists := s.streams[token]; exists {
			stream.Close()
			delete(s.streams, token)
			logger.Warnf("rtsp", "Publisher disconnected, stream closed: token='%s'", token)
		}
	}
}

// OnDescribe is called when receiving a DESCRIBE request.
func (s *Server) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	remoteAddr := remoteAddrOf(ctx.Conn)
	if resp := s.checkAuth(ctx.Request, false, remoteAddr); resp != nil {
		logger.Warnf("rtsp", "Unauthorized DESCRIBE from %s for path '%s'", remoteAddr, ctx.Path)
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
	if resp := s.checkAuth(ctx.Request, true, remoteAddrOf(ctx.Conn)); resp != nil {
		return resp, nil
	}

	token := normalizePath(ctx.Path)
	stream := &gortsplib.ServerStream{
		Server: s.rtspServer,
		Desc:   ctx.Description,
	}
	if err := stream.Initialize(); err != nil {
		logger.Errorf("rtsp", "Failed to initialize stream for token='%s': %v", token, err)
		return &base.Response{StatusCode: base.StatusInternalServerError}, nil
	}

	s.mu.Lock()
	if old, exists := s.streams[token]; exists {
		old.Close()
	}
	s.streams[token] = stream
	s.publishers[ctx.Session] = token
	s.mu.Unlock()

	logger.Infof("rtsp", "Stream announced: token='%s'", token)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// OnSetup is called when a client sets up a track.
func (s *Server) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	// Publishers SETUP their own tracks; no stream is attached on that side.
	if ctx.Session.State() == gortsplib.ServerSessionStatePreRecord {
		return &base.Response{StatusCode: base.StatusOK}, nil, nil
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

// OnPlay is called when a reader starts playing.
func (s *Server) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	token := normalizePath(ctx.Path)
	remoteIP := remoteHostOf(remoteAddrOf(ctx.Conn))

	s.mu.Lock()
	s.readerSeq++
	rec := &clientSessionRecord{
		id:        fmt.Sprintf("sess-%d", s.readerSeq),
		remoteIP:  remoteIP,
		path:      token,
		startTime: s.now(),
	}
	s.readers[ctx.Session] = rec
	active := len(s.readers)
	s.mu.Unlock()

	logger.Infof("rtsp", "Client started PLAY: stream='%s' from %s (Active readers: %d)", token, remoteIP, active)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// OnRecord is called when a publisher starts publishing.
func (s *Server) OnRecord(ctx *gortsplib.ServerHandlerOnRecordCtx) (*base.Response, error) {
	s.mu.RLock()
	token := s.publishers[ctx.Session]
	stream := s.streams[token]
	s.mu.RUnlock()

	if stream == nil {
		return &base.Response{StatusCode: base.StatusBadRequest}, nil
	}

	ctx.Session.OnPacketRTPAny(func(medi *description.Media, _ format.Format, pkt *rtp.Packet) {
		s.meter.Record(len(pkt.Payload))
		_ = stream.WritePacketRTP(medi, pkt)
	})
	ctx.Session.OnPacketRTCPAny(func(medi *description.Media, pkt rtcp.Packet) {
		_ = stream.WritePacketRTCP(medi, pkt)
	})

	logger.Infof("rtsp", "Publishing started for token='%s'", token)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// CloseStream closes a specific stream so that readers reconnect and receive
// a fresh SDP (used after a profile hot-reload).
func (s *Server) CloseStream(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.streams[token]; ok {
		st.Close()
		delete(s.streams, token)
		logger.Infof("rtsp", "Stream closed: token='%s'", token)
	}
}

// GetClients returns the list of connected RTSP reader sessions.
func (s *Server) GetClients() []ClientInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now()
	clients := make([]ClientInfo, 0, len(s.readers))
	for _, rec := range s.readers {
		clients = append(clients, ClientInfo{
			ID:        rec.id,
			RemoteIP:  rec.remoteIP,
			Path:      rec.path,
			Transport: "TCP/RTP",
			Duration:  int64(now.Sub(rec.startTime).Seconds()),
		})
	}
	return clients
}

// GetStats returns current streaming metrics.
func (s *Server) GetStats() StreamStats {
	packets, bytes, kbps := s.meter.Snapshot(s.now())

	s.mu.RLock()
	readers := len(s.readers)
	s.mu.RUnlock()

	return StreamStats{
		PacketsSent:   packets,
		BytesSent:     bytes,
		BitrateKbps:   kbps,
		ActiveReaders: readers,
	}
}
