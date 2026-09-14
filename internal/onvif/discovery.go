package onvif

import (
	"context"
	"encoding/xml"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/google/uuid"

	"mockcam/internal/config"
)

// DiscoveryServer handles ONVIF WS-Discovery (UDP 3702).
type DiscoveryServer struct {
	cfgMgr *config.Manager
	conn   *net.UDPConn
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewDiscoveryServer creates a new WS-Discovery listener.
func NewDiscoveryServer(cfgMgr *config.Manager) *DiscoveryServer {
	ctx, cancel := context.WithCancel(context.Background())
	return &DiscoveryServer{
		cfgMgr: cfgMgr,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start begins listening on UDP 3702 multicast 239.255.255.250.
func (d *DiscoveryServer) Start() error {
	cfg := d.cfgMgr.Get()
	port := cfg.Server.ONVIFPort
	if port <= 0 {
		port = 3702
	}

	multicastAddr := &net.UDPAddr{
		IP:   net.ParseIP("239.255.255.250"),
		Port: port,
	}

	conn, err := net.ListenMulticastUDP("udp4", nil, multicastAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on WS-Discovery multicast: %w", err)
	}

	d.conn = conn
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.listenLoop()
	}()

	log.Printf("[onvif] WS-Discovery listening on 239.255.255.250:%d", port)
	return nil
}

// Close stops the WS-Discovery server.
func (d *DiscoveryServer) Close() {
	d.cancel()
	if d.conn != nil {
		_ = d.conn.Close()
	}
	d.wg.Wait()
	log.Println("[onvif] WS-Discovery listener stopped")
}

func (d *DiscoveryServer) listenLoop() {
	buf := make([]byte, 8192)
	for {
		select {
		case <-d.ctx.Done():
			return
		default:
		}

		n, src, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-d.ctx.Done():
				return
			default:
				continue
			}
		}

		payload := buf[:n]
		if d.isProbe(payload) {
			go d.handleProbe(payload, src)
		}
	}
}

func (d *DiscoveryServer) isProbe(data []byte) bool { return IsProbe(data) }

// IsProbe reports whether a UDP datagram looks like a WS-Discovery Probe.
func IsProbe(data []byte) bool {
	str := string(data)
	return strings.Contains(str, "http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe") ||
		(strings.Contains(str, "Probe") && strings.Contains(str, "discovery"))
}

func (d *DiscoveryServer) handleProbe(data []byte, src *net.UDPAddr) {
	cfg := d.cfgMgr.Get()
	localIP := getOutboundIP(src.IP)

	fullPayload, err := BuildProbeMatches(data, localIP, cfg)
	if err != nil {
		log.Printf("[onvif] Failed to build ProbeMatches: %v", err)
		return
	}

	if _, err := d.conn.WriteToUDP(fullPayload, src); err != nil {
		log.Printf("[onvif] Failed to send ProbeMatches to %s: %v", src.String(), err)
		return
	}
	log.Printf("[onvif] Sent ProbeMatches to %s (XAddrs: http://%s:%d/onvif/device_service)", src.String(), localIP, cfg.Server.HTTPPort)
}

// ProbeMessageID extracts the WS-Addressing MessageID from a Probe so the
// reply can reference it in RelatesTo. A placeholder is returned when the
// probe cannot be parsed, which keeps lenient clients working.
func ProbeMessageID(probe []byte) string {
	var env ProbeEnvelope
	if err := xml.Unmarshal(probe, &env); err == nil && env.Header.MessageID != "" {
		return env.Header.MessageID
	}
	return "urn:uuid:00000000-0000-0000-0000-000000000000"
}

// BuildProbeMatches renders the WS-Discovery ProbeMatches reply for a Probe
// received from a client, advertising the device service at localIP.
func BuildProbeMatches(probe []byte, localIP string, cfg config.Config) ([]byte, error) {
	xAddr := fmt.Sprintf("http://%s:%d/onvif/device_service", localIP, cfg.Server.HTTPPort)

	resp := ProbeMatchesEnvelope{
		SoapAttr: "http://www.w3.org/2003/05/soap-envelope",
		WsaAttr:  "http://schemas.xmlsoap.org/ws/2004/08/addressing",
		DAttr:    "http://schemas.xmlsoap.org/ws/2005/04/discovery",
	}
	resp.Header.WsaAction = "http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches"
	resp.Header.WsaMessageID = fmt.Sprintf("urn:uuid:%s", uuid.New().String())
	resp.Header.WsaRelatesTo = ProbeMessageID(probe)
	resp.Header.WsaTo = "http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous"

	scopes := "onvif://www.onvif.org/type/NetworkVideoTransmitter " +
		"onvif://www.onvif.org/name/MockCam " +
		"onvif://www.onvif.org/hardware/" + cfg.Server.DeviceInfo.Model + " " +
		"onvif://www.onvif.org/location/any"

	item := ProbeMatchItem{
		Types:           "dn:NetworkVideoTransmitter tds:Device",
		Scopes:          scopes,
		XAddrs:          xAddr,
		MetadataVersion: 1,
	}
	item.EndpointReference.Address = fmt.Sprintf("urn:uuid:%s", cfg.Server.DeviceInfo.SerialNumber)
	resp.Body.ProbeMatches.ProbeMatch = []ProbeMatchItem{item}

	respBytes, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), respBytes...), nil
}

func getOutboundIP(dest net.IP) string {
	conn, err := net.Dial("udp", fmt.Sprintf("%s:80", dest.String()))
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}
