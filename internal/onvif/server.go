package onvif

import (
	"bytes"
	"encoding/xml"
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
}

// NewServer creates a new ONVIF SOAP Server.
func NewServer(cfgMgr *config.Manager, authenticator *auth.Authenticator, ptz *PTZController) *Server {
	return &Server{
		cfgMgr:        cfgMgr,
		auth:          authenticator,
		ptz:           ptz,
		deviceHandler: NewDeviceHandler(cfgMgr),
		mediaHandler:  NewMediaHandler(cfgMgr),
	}
}

// RegisterRoutes registers ONVIF SOAP endpoints on an http.ServeMux.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/onvif/device_service", s.handleDeviceService)
	mux.HandleFunc("/onvif/media_service", s.handleMediaService)
	mux.HandleFunc("/onvif/ptz_service", s.handlePTZService)
}

func (s *Server) getRequestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func (s *Server) handleDeviceService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, action, err := s.readSOAPRequest(r)
	if err != nil {
		s.writeSOAPFault(w, "Sender", "Invalid XML: "+err.Error())
		return
	}

	// For sensitive calls, verify auth
	if action != "GetSystemDateAndTime" && action != "GetCapabilities" {
		if !s.auth.CheckHTTP(w, r) {
			return
		}
	}

	host := s.getRequestHost(r)
	var respXML string

	switch action {
	case "GetDeviceInformation":
		respXML = s.deviceHandler.HandleGetDeviceInformation()
	case "GetSystemDateAndTime":
		respXML = s.deviceHandler.HandleGetSystemDateAndTime()
	case "GetCapabilities":
		respXML = s.deviceHandler.HandleGetCapabilities(host)
	case "GetServices":
		respXML = s.deviceHandler.HandleGetServices(host)
	case "GetScopes":
		respXML = s.deviceHandler.HandleGetScopes()
	case "GetHostname":
		respXML = s.deviceHandler.HandleGetHostname()
	default:
		log.Printf("[onvif] Unhandled device action: %s", action)
		s.writeSOAPFault(w, "ActionNotSupported", "Action not supported: "+action)
		return
	}

	s.writeSOAPResponse(w, respXML)
	_ = body
}

func (s *Server) handleMediaService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.auth.CheckHTTP(w, r) {
		return
	}

	body, action, err := s.readSOAPRequest(r)
	if err != nil {
		s.writeSOAPFault(w, "Sender", "Invalid XML: "+err.Error())
		return
	}

	host := s.getRequestHost(r)
	var respXML string

	switch action {
	case "GetProfiles":
		respXML = s.mediaHandler.HandleGetProfiles()
	case "GetProfile":
		respXML = s.mediaHandler.HandleGetProfile(body)
	case "GetStreamUri":
		respXML = s.mediaHandler.HandleGetStreamUri(body, host)
	case "GetSnapshotUri":
		respXML = s.mediaHandler.HandleGetSnapshotUri(body, host)
	case "GetVideoEncoderConfigurations":
		respXML = s.mediaHandler.HandleGetVideoEncoderConfigurations()
	case "GetVideoSources":
		respXML = s.mediaHandler.HandleGetVideoSources()
	default:
		log.Printf("[onvif] Unhandled media action: %s", action)
		s.writeSOAPFault(w, "ActionNotSupported", "Action not supported: "+action)
		return
	}

	s.writeSOAPResponse(w, respXML)
}

func (s *Server) handlePTZService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.auth.CheckHTTP(w, r) {
		return
	}

	body, action, err := s.readSOAPRequest(r)
	if err != nil {
		s.writeSOAPFault(w, "Sender", "Invalid XML: "+err.Error())
		return
	}

	var respXML string

	switch action {
	case "GetStatus":
		pan, tilt, zoom, moving := s.ptz.GetStatus()
		moveStatus := "IDLE"
		if moving {
			moveStatus = "MOVING"
		}
		respXML = fmt.Sprintf(`
			<tptz:GetStatusResponse xmlns:tptz="%s" xmlns:tt="%s">
				<tptz:PTZStatus>
					<tt:Position>
						<tt:PanTilt x="%.4f" y="%.4f" space="http://www.onvif.org/ver10/tptz/PanTiltSpaces/PositionGenericSpace" />
						<tt:Zoom x="%.4f" space="http://www.onvif.org/ver10/tptz/ZoomSpaces/PositionGenericSpace" />
					</tt:Position>
					<tt:MoveStatus>
						<tt:PanTilt>%s</tt:PanTilt>
						<tt:Zoom>%s</tt:Zoom>
					</tt:MoveStatus>
					<tt:UtcTime>%s</tt:UtcTime>
				</tptz:PTZStatus>
			</tptz:GetStatusResponse>`,
			NamespacePTZWSDL,
			NamespaceONVIFSchema,
			pan, tilt, zoom,
			moveStatus, moveStatus,
			time.Now().UTC().Format(time.RFC3339),
		)

	case "ContinuousMove":
		velX, velY, velZ := parsePTZCoords(body, "Velocity")
		s.ptz.ContinuousMove(velX, velY, velZ)
		respXML = fmt.Sprintf(`<tptz:ContinuousMoveResponse xmlns:tptz="%s" />`, NamespacePTZWSDL)

	case "AbsoluteMove":
		posX, posY, posZ := parsePTZCoords(body, "Position")
		s.ptz.AbsoluteMove(posX, posY, posZ)
		respXML = fmt.Sprintf(`<tptz:AbsoluteMoveResponse xmlns:tptz="%s" />`, NamespacePTZWSDL)

	case "Stop":
		s.ptz.Stop()
		respXML = fmt.Sprintf(`<tptz:StopResponse xmlns:tptz="%s" />`, NamespacePTZWSDL)

	case "GetNodes":
		cfg := s.cfgMgr.Get()
		respXML = fmt.Sprintf(`
			<tptz:GetNodesResponse xmlns:tptz="%s" xmlns:tt="%s">
				<tptz:PTZNode token="%s">
					<tt:Name>PTZNode_1</tt:Name>
					<tt:SupportedPTZSpaces>
						<tt:AbsolutePanTiltPositionSpace>
							<tt:URI>http://www.onvif.org/ver10/tptz/PanTiltSpaces/PositionGenericSpace</tt:URI>
							<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
							<tt:YRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:YRange>
						</tt:AbsolutePanTiltPositionSpace>
						<tt:AbsoluteZoomPositionSpace>
							<tt:URI>http://www.onvif.org/ver10/tptz/ZoomSpaces/PositionGenericSpace</tt:URI>
							<tt:XRange><tt:Min>0.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
						</tt:AbsoluteZoomPositionSpace>
					</tt:SupportedPTZSpaces>
					<tt:MaximumNumberOfPresets>10</tt:MaximumNumberOfPresets>
					<tt:HomeSupported>true</tt:HomeSupported>
				</tptz:PTZNode>
			</tptz:GetNodesResponse>`,
			NamespacePTZWSDL, NamespaceONVIFSchema, cfg.PTZ.NodeToken,
		)

	case "GetConfigurations":
		cfg := s.cfgMgr.Get()
		respXML = fmt.Sprintf(`
			<tptz:GetConfigurationsResponse xmlns:tptz="%s" xmlns:tt="%s">
				<tptz:PTZConfiguration token="PTZ_Profile_1">
					<tt:Name>PTZConfig</tt:Name>
					<tt:UseCount>1</tt:UseCount>
					<tt:NodeToken>%s</tt:NodeToken>
				</tptz:PTZConfiguration>
			</tptz:GetConfigurationsResponse>`,
			NamespacePTZWSDL, NamespaceONVIFSchema, cfg.PTZ.NodeToken,
		)

	default:
		log.Printf("[onvif] Unhandled PTZ action: %s", action)
		s.writeSOAPFault(w, "ActionNotSupported", "Action not supported: "+action)
		return
	}

	s.writeSOAPResponse(w, respXML)
}

func parsePTZCoords(data []byte, parentTag string) (float64, float64, float64) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var pan, tilt, zoom float64
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
					for _, a := range elem.Attr {
						if a.Name.Local == "x" {
							pan, _ = strconv.ParseFloat(a.Value, 64)
						} else if a.Name.Local == "y" {
							tilt, _ = strconv.ParseFloat(a.Value, 64)
						}
					}
				} else if elem.Name.Local == "Zoom" {
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
	return pan, tilt, zoom
}

func (s *Server) readSOAPRequest(r *http.Request) ([]byte, string, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", err
	}

	// 1. Check SOAPAction header
	soapAction := r.Header.Get("SOAPAction")
	if soapAction != "" {
		soapAction = strings.Trim(soapAction, `"`)
		if slashIdx := strings.LastIndex(soapAction, "/"); slashIdx != -1 {
			return body, soapAction[slashIdx+1:], nil
		}
		return body, soapAction, nil
	}

	// 2. Parse XML to detect Body element root tag
	decoder := xml.NewDecoder(bytes.NewReader(body))
	inBody := false
	for {
		t, err := decoder.Token()
		if err != nil {
			break
		}
		switch elem := t.(type) {
		case xml.StartElement:
			if elem.Name.Local == "Body" {
				inBody = true
				continue
			}
			if inBody {
				return body, elem.Name.Local, nil
			}
		}
	}

	return body, "", nil
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

func (s *Server) writeSOAPFault(w http.ResponseWriter, code, reason string) {
	fault := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="%s">
	<s:Body>
		<s:Fault>
			<s:Code><s:Value>s:%s</s:Value></s:Code>
			<s:Reason><s:Text xml:lang="en">%s</s:Text></s:Reason>
		</s:Fault>
	</s:Body>
</s:Envelope>`,
		NamespaceSOAPEnv,
		code,
		reason,
	)

	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte(fault))
}
