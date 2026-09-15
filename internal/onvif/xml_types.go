package onvif

import (
	"encoding/xml"
)

// Standard ONVIF and SOAP namespaces
const (
	NamespaceSOAPEnv     = "http://www.w3.org/2003/05/soap-envelope"
	NamespaceSOAP11      = "http://schemas.xmlsoap.org/soap/envelope/"
	NamespaceWSA         = "http://schemas.xmlsoap.org/ws/2004/08/addressing"
	NamespaceWSA2005     = "http://www.w3.org/2005/08/addressing"
	NamespaceDiscovery   = "http://schemas.xmlsoap.org/ws/2005/04/discovery"
	NamespaceONVIFSchema = "http://www.onvif.org/ver10/schema"
	NamespaceDeviceWSDL  = "http://www.onvif.org/ver10/device/wsdl"
	NamespaceMediaWSDL   = "http://www.onvif.org/ver10/media/wsdl"
	NamespacePTZWSDL     = "http://www.onvif.org/ver20/ptz/wsdl"
	NamespaceNetworkWSDL = "http://www.onvif.org/ver10/network/wsdl"
	NamespaceONVIFError  = "http://www.onvif.org/ver10/error"
)

// SOAPEnvelope represents a generic SOAP envelope for parsing.
type SOAPEnvelope struct {
	XMLName xml.Name   `xml:"Envelope"`
	Header  SOAPHeader `xml:"Header"`
	Body    SOAPBody   `xml:"Body"`
}

// SOAPHeader represents the SOAP header.
type SOAPHeader struct {
	Action    string `xml:"Action"`
	MessageID string `xml:"MessageID"`
	To        string `xml:"To"`
	RelatesTo string `xml:"RelatesTo"`
}

// SOAPBody contains the raw inner XML payload.
type SOAPBody struct {
	Content []byte `xml:",innerxml"`
}

// SOAPFaultEnvelope represents a SOAP fault.
type SOAPFaultEnvelope struct {
	XMLName xml.Name      `xml:"http://www.w3.org/2003/05/soap-envelope Envelope"`
	Body    SOAPFaultBody `xml:"Body"`
}

// SOAPFaultBody is the body of a SOAP fault.
type SOAPFaultBody struct {
	Fault SOAPFault `xml:"Fault"`
}

// SOAPFault contains fault details.
type SOAPFault struct {
	Code   SOAPFaultCode   `xml:"Code"`
	Reason SOAPFaultReason `xml:"Reason"`
}

// SOAPFaultCode contains value.
type SOAPFaultCode struct {
	Value string `xml:"Value"`
}

// SOAPFaultReason contains text.
type SOAPFaultReason struct {
	Text string `xml:"Text"`
}

// --- WS-Discovery types ---

// ProbeEnvelope represents an incoming probe envelope.
type ProbeEnvelope struct {
	XMLName xml.Name   `xml:"Envelope"`
	Header  SOAPHeader `xml:"Header"`
	Body    struct {
		Probe ProbeType `xml:"Probe"`
	} `xml:"Body"`
}

// ProbeType represents the Probe body.
type ProbeType struct {
	Types  string `xml:"Types"`
	Scopes string `xml:"Scopes"`
}

// ProbeMatchesEnvelope is the response sent to WS-Discovery Probes.
type ProbeMatchesEnvelope struct {
	XMLName  xml.Name `xml:"soap:Envelope"`
	SoapAttr string   `xml:"xmlns:soap,attr"`
	WsaAttr  string   `xml:"xmlns:wsa,attr"`
	DAttr    string   `xml:"xmlns:d,attr"`
	DnAttr   string   `xml:"xmlns:dn,attr"`
	TdsAttr  string   `xml:"xmlns:tds,attr"`
	Header   struct {
		WsaAction    string `xml:"wsa:Action"`
		WsaMessageID string `xml:"wsa:MessageID"`
		WsaRelatesTo string `xml:"wsa:RelatesTo"`
		WsaTo        string `xml:"wsa:To"`
	} `xml:"soap:Header"`
	Body struct {
		ProbeMatches struct {
			ProbeMatch []ProbeMatchItem `xml:"d:ProbeMatch"`
		} `xml:"d:ProbeMatches"`
	} `xml:"soap:Body"`
}

// ProbeMatchItem represents a single discovered device in ProbeMatches.
type ProbeMatchItem struct {
	EndpointReference struct {
		Address string `xml:"wsa:Address"`
	} `xml:"wsa:EndpointReference"`
	Types           string `xml:"d:Types"`
	Scopes          string `xml:"d:Scopes"`
	XAddrs          string `xml:"d:XAddrs"`
	MetadataVersion int    `xml:"d:MetadataVersion"`
}

// --- ONVIF Schema types ---

// Vector2D represents a 2D float vector.
type Vector2D struct {
	X     float64 `xml:"x,attr"`
	Y     float64 `xml:"y,attr"`
	Space string  `xml:"space,attr,omitempty"`
}

// Vector1D represents a 1D float vector.
type Vector1D struct {
	X     float64 `xml:"x,attr"`
	Space string  `xml:"space,attr,omitempty"`
}

// PTZVector represents pan/tilt and zoom coordinates.
type PTZVector struct {
	PanTilt *Vector2D `xml:"tt:PanTilt,omitempty"`
	Zoom    *Vector1D `xml:"tt:Zoom,omitempty"`
}

// PTZStatus represents the current status of PTZ.
type PTZStatus struct {
	Position   *PTZVector `xml:"tt:Position,omitempty"`
	MoveStatus struct {
		PanTilt string `xml:"tt:PanTilt"`
		Zoom    string `xml:"tt:Zoom"`
	} `xml:"tt:MoveStatus"`
	UtcTime string `xml:"tt:UtcTime"`
}

// VideoResolution represents video dimensions.
type VideoResolution struct {
	Width  int `xml:"tt:Width"`
	Height int `xml:"tt:Height"`
}

// VideoRateControl represents bitrate and framerate settings.
type VideoRateControl struct {
	FrameRateLimit   int `xml:"tt:FrameRateLimit"`
	EncodingInterval int `xml:"tt:EncodingInterval"`
	BitrateLimit     int `xml:"tt:BitrateLimit"`
}

// H264Configuration represents H.264 settings.
type H264Configuration struct {
	GovLength   int    `xml:"tt:GovLength"`
	H264Profile string `xml:"tt:H264Profile"`
}

// VideoEncoderConfiguration represents a video encoder config in ONVIF.
type VideoEncoderConfiguration struct {
	Token       string             `xml:"token,attr"`
	Name        string             `xml:"tt:Name"`
	UseCount    int                `xml:"tt:UseCount"`
	Encoding    string             `xml:"tt:Encoding"`
	Resolution  VideoResolution    `xml:"tt:Resolution"`
	Quality     float64            `xml:"tt:Quality"`
	RateControl VideoRateControl   `xml:"tt:RateControl"`
	H264        *H264Configuration `xml:"tt:H264,omitempty"`
	Multicast   struct {
		Address struct {
			Type        string `xml:"tt:Type"`
			IPv4Address string `xml:"tt:IPv4Address"`
		} `xml:"tt:Address"`
		Port      int  `xml:"tt:Port"`
		TTL       int  `xml:"tt:TTL"`
		AutoStart bool `xml:"tt:AutoStart"`
	} `xml:"tt:Multicast"`
	SessionTimeout string `xml:"tt:SessionTimeout"`
}

// AudioEncoderConfiguration represents an audio encoder config in ONVIF.
type AudioEncoderConfiguration struct {
	Token      string `xml:"token,attr"`
	Name       string `xml:"tt:Name"`
	UseCount   int    `xml:"tt:UseCount"`
	Encoding   string `xml:"tt:Encoding"`
	Bitrate    int    `xml:"tt:Bitrate"`
	SampleRate int    `xml:"tt:SampleRate"`
	Multicast  struct {
		Address struct {
			Type        string `xml:"tt:Type"`
			IPv4Address string `xml:"tt:IPv4Address"`
		} `xml:"tt:Address"`
		Port      int  `xml:"tt:Port"`
		TTL       int  `xml:"tt:TTL"`
		AutoStart bool `xml:"tt:AutoStart"`
	} `xml:"tt:Multicast"`
	SessionTimeout string `xml:"tt:SessionTimeout"`
}

// PTZConfiguration represents a PTZ node configuration.
type PTZConfiguration struct {
	Token     string `xml:"token,attr"`
	Name      string `xml:"tt:Name"`
	UseCount  int    `xml:"tt:UseCount"`
	NodeToken string `xml:"tt:NodeToken"`
}

// Profile represents an ONVIF Profile S profile.
type Profile struct {
	Token                     string                     `xml:"token,attr"`
	Fixed                     bool                       `xml:"fixed,attr"`
	Name                      string                     `xml:"tt:Name"`
	VideoSourceConfiguration  *VideoSourceConfiguration  `xml:"tt:VideoSourceConfiguration,omitempty"`
	AudioSourceConfiguration  *AudioSourceConfiguration  `xml:"tt:AudioSourceConfiguration,omitempty"`
	VideoEncoderConfiguration *VideoEncoderConfiguration `xml:"tt:VideoEncoderConfiguration,omitempty"`
	AudioEncoderConfiguration *AudioEncoderConfiguration `xml:"tt:AudioEncoderConfiguration,omitempty"`
	PTZConfiguration          *PTZConfiguration          `xml:"tt:PTZConfiguration,omitempty"`
}

// VideoSourceConfiguration represents video source.
type VideoSourceConfiguration struct {
	Token       string `xml:"token,attr"`
	Name        string `xml:"tt:Name"`
	UseCount    int    `xml:"tt:UseCount"`
	SourceToken string `xml:"tt:SourceToken"`
	Bounds      struct {
		X      int `xml:"x,attr"`
		Y      int `xml:"y,attr"`
		Width  int `xml:"width,attr"`
		Height int `xml:"height,attr"`
	} `xml:"tt:Bounds"`
}

// AudioSourceConfiguration represents audio source.
type AudioSourceConfiguration struct {
	Token       string `xml:"token,attr"`
	Name        string `xml:"tt:Name"`
	UseCount    int    `xml:"tt:UseCount"`
	SourceToken string `xml:"tt:SourceToken"`
}

// MediaURI represents stream URI response.
type MediaURI struct {
	URI                 string `xml:"tt:Uri"`
	InvalidAfterConnect bool   `xml:"tt:InvalidAfterConnect"`
	InvalidAfterReboot  bool   `xml:"tt:InvalidAfterReboot"`
	Timeout             string `xml:"tt:Timeout"`
}
