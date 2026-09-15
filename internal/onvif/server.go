package onvif

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mockcam/internal/auth"
	"mockcam/internal/config"
)

// Server coordinates the ONVIF SOAP services.
type Server struct {
	cfgMgr        *config.Manager
	auth          *auth.Authenticator
	ptz           *PTZController
	deviceHandler *DeviceHandler
	mediaHandler  *MediaHandler
	ptzHandler    *PTZHandler
	now           func() time.Time
}

// NewServer creates a new ONVIF SOAP Server.
func NewServer(cfgMgr *config.Manager, authenticator *auth.Authenticator, ptz *PTZController) *Server {
	return &Server{
		cfgMgr:        cfgMgr,
		auth:          authenticator,
		ptz:           ptz,
		deviceHandler: NewDeviceHandler(cfgMgr),
		mediaHandler:  NewMediaHandler(cfgMgr),
		ptzHandler:    NewPTZHandler(cfgMgr, ptz),
		now:           time.Now,
	}
}

// RegisterRoutes registers ONVIF SOAP endpoints on an http.ServeMux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/onvif/device_service", s.handleDeviceService)
	mux.HandleFunc("/onvif/media_service", s.handleMediaService)
	mux.HandleFunc("/onvif/ptz_service", s.handlePTZService)
}

// preAuthDeviceActions may be called without credentials. ONVIF clients use
// GetSystemDateAndTime to synchronise their clock before they can build a
// WS-UsernameToken, and GetCapabilities to locate the other services.
var preAuthDeviceActions = map[string]bool{
	"GetSystemDateAndTime": true,
	"GetCapabilities":      true,
}

// soapFault is an ONVIF-style SOAP 1.2 fault: env:Sender / env:Receiver with a
// ter:* subcode. It is returned by handlers that validate request arguments.
type soapFault struct {
	status  int
	code    string // "Sender" or "Receiver"
	subcode string // e.g. "ter:NoProfile"
	reason  string
}

func (f *soapFault) Error() string { return f.subcode + ": " + f.reason }

func senderFault(subcode, reason string) *soapFault {
	return &soapFault{status: http.StatusBadRequest, code: "Sender", subcode: subcode, reason: reason}
}

func (s *Server) getRequestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// authorize enforces the configured authentication on a SOAP request. It
// accepts HTTP Basic/Digest (Authorization header) as well as a WS-Security
// UsernameToken in the SOAP header, which is what ONVIF clients send. Without
// any credentials it answers 401 with a WWW-Authenticate challenge (so HTTP
// clients retry) and a ter:NotAuthorized fault body (so SOAP clients get XML).
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, body []byte) bool {
	if !auth.IsAuthEnabled(s.cfgMgr.Get().Server.AuthType) {
		return true
	}
	if r.Header.Get("Authorization") != "" {
		return s.auth.CheckHTTP(w, r)
	}
	if tok, ok := auth.ParseUsernameToken(body); ok {
		if s.auth.ValidateUsernameToken(tok, s.now()) {
			return true
		}
		log.Printf("[onvif] Rejected WS-UsernameToken for user %q from %s", tok.Username, r.RemoteAddr)
		s.writeSOAPFault(w, http.StatusBadRequest, "Sender", "ter:NotAuthorized", "The credentials in the WS-UsernameToken are not valid")
		return false
	}
	w.Header().Set("WWW-Authenticate", s.auth.Challenge())
	s.writeSOAPFault(w, http.StatusUnauthorized, "Sender", "ter:NotAuthorized", "Authentication required (HTTP Basic/Digest or WS-UsernameToken)")
	return false
}

// readRequest validates the method, reads the envelope and resolves the action.
func (s *Server) readRequest(w http.ResponseWriter, r *http.Request) (body []byte, action string, ok bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return nil, "", false
	}
	body, action, err := s.readSOAPRequest(r)
	if err != nil {
		s.writeSOAPFault(w, http.StatusBadRequest, "Sender", "ter:WellFormed", "Invalid XML: "+err.Error())
		return nil, "", false
	}
	return body, action, true
}

// finish writes either the handler's response or the fault it returned.
func (s *Server) finish(w http.ResponseWriter, service, action, respXML string, err error) {
	var f *soapFault
	switch {
	case err == nil:
		s.writeSOAPResponse(w, respXML)
	case errors.As(err, &f):
		s.writeSOAPFault(w, f.status, f.code, f.subcode, f.reason)
	default:
		log.Printf("[onvif] %s %s failed: %v", service, action, err)
		s.writeSOAPFault(w, http.StatusInternalServerError, "Receiver", "ter:Action", err.Error())
	}
}

func (s *Server) notSupported(w http.ResponseWriter, service, action string) {
	log.Printf("[onvif] Unhandled %s action: %s", service, action)
	s.writeSOAPFault(w, http.StatusInternalServerError, "Receiver", "ter:ActionNotSupported", "Action not supported: "+action)
}

func (s *Server) handleDeviceService(w http.ResponseWriter, r *http.Request) {
	body, action, ok := s.readRequest(w, r)
	if !ok {
		return
	}
	if !preAuthDeviceActions[action] && !s.authorize(w, r, body) {
		return
	}

	host := s.getRequestHost(r)
	h := s.deviceHandler
	var respXML string

	switch action {
	case "GetDeviceInformation":
		respXML = h.HandleGetDeviceInformation()
	case "GetSystemDateAndTime":
		respXML = h.HandleGetSystemDateAndTime()
	case "GetCapabilities":
		respXML = h.HandleGetCapabilities(host)
	case "GetServices":
		respXML = h.HandleGetServices(host)
	case "GetServiceCapabilities":
		respXML = h.HandleGetServiceCapabilities()
	case "GetScopes":
		respXML = h.HandleGetScopes()
	case "GetHostname":
		respXML = h.HandleGetHostname()
	case "GetNetworkInterfaces":
		respXML = h.HandleGetNetworkInterfaces(host)
	case "GetNetworkProtocols":
		respXML = h.HandleGetNetworkProtocols()
	case "GetDNS":
		respXML = h.HandleGetDNS()
	case "GetNTP":
		respXML = h.HandleGetNTP()
	case "GetDiscoveryMode":
		respXML = h.HandleGetDiscoveryMode()
	case "GetUsers":
		respXML = h.HandleGetUsers()
	case "GetWsdlUrl":
		respXML = h.HandleGetWsdlUrl()
	default:
		s.notSupported(w, "device", action)
		return
	}
	s.finish(w, "device", action, respXML, nil)
}

func (s *Server) handleMediaService(w http.ResponseWriter, r *http.Request) {
	body, action, ok := s.readRequest(w, r)
	if !ok {
		return
	}
	if !s.authorize(w, r, body) {
		return
	}

	host := s.getRequestHost(r)
	h := s.mediaHandler
	var respXML string
	var err error

	switch action {
	case "GetProfiles":
		respXML = h.HandleGetProfiles()
	case "GetProfile":
		respXML, err = h.HandleGetProfile(body)
	case "GetStreamUri":
		respXML, err = h.HandleGetStreamUri(body, host)
	case "GetSnapshotUri":
		respXML, err = h.HandleGetSnapshotUri(body, host)
	case "GetVideoSources":
		respXML = h.HandleGetVideoSources()
	case "GetVideoSourceConfigurations":
		respXML = h.HandleGetVideoSourceConfigurations()
	case "GetVideoSourceConfiguration":
		respXML, err = h.HandleGetVideoSourceConfiguration(body)
	case "GetVideoEncoderConfigurations":
		respXML = h.HandleGetVideoEncoderConfigurations()
	case "GetVideoEncoderConfiguration":
		respXML, err = h.HandleGetVideoEncoderConfiguration(body)
	case "GetVideoEncoderConfigurationOptions":
		respXML, err = h.HandleGetVideoEncoderConfigurationOptions(body)
	case "GetAudioSources":
		respXML = h.HandleGetAudioSources()
	case "GetAudioSourceConfigurations":
		respXML = h.HandleGetAudioSourceConfigurations()
	case "GetAudioEncoderConfigurations":
		respXML = h.HandleGetAudioEncoderConfigurations()
	case "GetAudioEncoderConfiguration":
		respXML, err = h.HandleGetAudioEncoderConfiguration(body)
	case "GetServiceCapabilities":
		respXML = h.HandleGetServiceCapabilities()
	default:
		s.notSupported(w, "media", action)
		return
	}
	s.finish(w, "media", action, respXML, err)
}

func (s *Server) handlePTZService(w http.ResponseWriter, r *http.Request) {
	body, action, ok := s.readRequest(w, r)
	if !ok {
		return
	}
	if !s.authorize(w, r, body) {
		return
	}

	h := s.ptzHandler
	var respXML string
	var err error

	switch action {
	case "GetStatus":
		respXML = h.HandleGetStatus(s.now())
	case "ContinuousMove":
		respXML = h.HandleContinuousMove(body)
	case "AbsoluteMove":
		respXML = h.HandleAbsoluteMove(body)
	case "RelativeMove":
		respXML = h.HandleRelativeMove(body)
	case "Stop":
		respXML = h.HandleStop()
	case "GotoHomePosition":
		respXML = h.HandleGotoHomePosition()
	case "SetHomePosition":
		respXML = h.HandleSetHomePosition()
	case "GetPresets":
		respXML = h.HandleGetPresets()
	case "SetPreset":
		respXML, err = h.HandleSetPreset(body)
	case "GotoPreset":
		respXML, err = h.HandleGotoPreset(body)
	case "RemovePreset":
		respXML, err = h.HandleRemovePreset(body)
	case "GetNodes":
		respXML = h.HandleGetNodes()
	case "GetNode":
		respXML, err = h.HandleGetNode(body)
	case "GetConfigurations":
		respXML = h.HandleGetConfigurations()
	case "GetConfiguration":
		respXML, err = h.HandleGetConfiguration(body)
	case "GetConfigurationOptions":
		respXML, err = h.HandleGetConfigurationOptions(body)
	case "GetServiceCapabilities":
		respXML = h.HandleGetServiceCapabilities()
	default:
		s.notSupported(w, "ptz", action)
		return
	}
	s.finish(w, "ptz", action, respXML, err)
}

// parsePTZCoords returns the x/y of the PanTilt element and x of the Zoom
// element found under parentTag (Position, Velocity or Translation).
func parsePTZCoords(data []byte, parentTag string) (pan, tilt, zoom float64) {
	pan, tilt, zoom, _, _ = parsePTZVector(data, parentTag)
	return pan, tilt, zoom
}

// parsePTZVector is parsePTZCoords that also reports whether the PanTilt and
// Zoom elements were present, so callers can distinguish "0" from "omitted".
func parsePTZVector(data []byte, parentTag string) (pan, tilt, zoom float64, hasPanTilt, hasZoom bool) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	inParent := false

	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch elem := token.(type) {
		case xml.StartElement:
			if elem.Name.Local == parentTag {
				inParent = true
			}
			if inParent {
				if elem.Name.Local == "PanTilt" {
					hasPanTilt = true
					for _, a := range elem.Attr {
						if a.Name.Local == "x" {
							pan, _ = strconv.ParseFloat(a.Value, 64)
						} else if a.Name.Local == "y" {
							tilt, _ = strconv.ParseFloat(a.Value, 64)
						}
					}
				} else if elem.Name.Local == "Zoom" {
					hasZoom = true
					for _, a := range elem.Attr {
						if a.Name.Local == "x" {
							zoom, _ = strconv.ParseFloat(a.Value, 64)
						}
					}
				}
			}
		case xml.EndElement:
			if elem.Name.Local == parentTag {
				inParent = false
			}
		}
	}
	return pan, tilt, zoom, hasPanTilt, hasZoom
}

// readSOAPRequest reads the envelope and resolves the action name from the
// first element inside the SOAP Body. The SOAPAction header / Content-Type
// action parameter is only a fallback: several ONVIF WSDLs declare
// malformed soapAction URIs (e.g. ".../wsdlGetVideoSources/"), so the body
// element is the reliable source.
func (s *Server) readSOAPRequest(r *http.Request) ([]byte, string, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", err
	}

	if action := bodyAction(body); action != "" {
		return body, action, nil
	}

	soapAction := strings.Trim(strings.TrimSpace(r.Header.Get("SOAPAction")), `"`)
	soapAction = strings.TrimRight(soapAction, "/")
	if idx := strings.LastIndex(soapAction, "/"); idx != -1 {
		soapAction = soapAction[idx+1:]
	}
	return body, soapAction, nil
}

// bodyAction returns the local name of the first element inside the SOAP
// Body, or "" when the envelope has none.
func bodyAction(body []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	inBody := false
	for {
		t, err := decoder.Token()
		if err != nil {
			return ""
		}
		if elem, ok := t.(xml.StartElement); ok {
			if elem.Name.Local == "Body" {
				inBody = true
				continue
			}
			if inBody {
				return elem.Name.Local
			}
		}
	}
}

func (s *Server) writeSOAPResponse(w http.ResponseWriter, innerXML string) {
	envelope := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s" xmlns:tt="%s">
	<s:Body>
		%s
	</s:Body>
</s:Envelope>`,
		NamespaceSOAPEnv,
		NamespaceONVIFSchema,
		strings.TrimSpace(innerXML),
	)

	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(envelope))
}

// writeSOAPFault emits a SOAP 1.2 fault. code is "Sender" or "Receiver";
// subcode is an ONVIF error code such as "ter:NotAuthorized". A two-level
// code such as "ter:InvalidArgVal/ter:NoProfile" is rendered as nested
// Subcode elements, as the ONVIF core specification requires.
func (s *Server) writeSOAPFault(w http.ResponseWriter, status int, code, subcode, reason string) {
	fault := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s" xmlns:ter="%s">
	<s:Body>
		<s:Fault>
			<s:Code>
				<s:Value>s:%s</s:Value>
				%s
			</s:Code>
			<s:Reason><s:Text xml:lang="en">%s</s:Text></s:Reason>
		</s:Fault>
	</s:Body>
</s:Envelope>`,
		NamespaceSOAPEnv,
		NamespaceONVIFError,
		code,
		renderSubcodes(strings.Split(subcode, "/")),
		xmlEscape(reason),
	)

	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(fault))
}

// renderSubcodes nests the given codes as s:Subcode elements.
func renderSubcodes(codes []string) string {
	if len(codes) == 0 || codes[0] == "" {
		return ""
	}
	return "<s:Subcode><s:Value>" + xmlEscape(codes[0]) + "</s:Value>" + renderSubcodes(codes[1:]) + "</s:Subcode>"
}

// xmlEscape escapes s for use as XML text or attribute content.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}
