package onvif

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"mockcam/internal/config"
)

// MediaHandler handles ONVIF Media service SOAP requests.
type MediaHandler struct {
	cfgMgr *config.Manager
}

// NewMediaHandler creates a new MediaHandler.
func NewMediaHandler(cfgMgr *config.Manager) *MediaHandler {
	return &MediaHandler{cfgMgr: cfgMgr}
}

// HandleGetProfiles generates response for GetProfiles.
func (h *MediaHandler) HandleGetProfiles() string {
	cfg := h.cfgMgr.Get()

	var profilesXML strings.Builder
	for _, p := range cfg.Profiles {
		profilesXML.WriteString(h.renderProfileXML(p, cfg.PTZ))
	}

	return fmt.Sprintf(`
		<trt:GetProfilesResponse xmlns:trt="%s" xmlns:tt="%s">
			%s
		</trt:GetProfilesResponse>`,
		NamespaceMediaWSDL,
		NamespaceONVIFSchema,
		profilesXML.String(),
	)
}

// HandleGetProfile generates response for a single GetProfile request.
func (h *MediaHandler) HandleGetProfile(body []byte) string {
	token := extractTagValue(body, "ProfileToken")
	cfg := h.cfgMgr.Get()

	prof, ok := h.cfgMgr.GetProfile(token)
	if !ok {
		if len(cfg.Profiles) > 0 {
			prof = cfg.Profiles[0]
		}
	}

	return fmt.Sprintf(`
		<trt:GetProfileResponse xmlns:trt="%s" xmlns:tt="%s">
			%s
		</trt:GetProfileResponse>`,
		NamespaceMediaWSDL,
		NamespaceONVIFSchema,
		h.renderProfileXML(prof, cfg.PTZ),
	)
}

func (h *MediaHandler) renderProfileXML(p config.ProfileConfig, ptz config.PTZConfig) string {
	var buf bytes.Buffer

	// Video Source Configuration
	vsc := fmt.Sprintf(`
		<tt:VideoSourceConfiguration token="VSC_%s">
			<tt:Name>VideoSourceConfig_%s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:SourceToken>VideoSource_1</tt:SourceToken>
			<tt:Bounds x="0" y="0" width="%d" height="%d" />
		</tt:VideoSourceConfiguration>`,
		p.Token, p.Token, p.Video.Resolution.Width, p.Video.Resolution.Height,
	)

	// Audio Source Configuration
	var asc string
	if p.Audio.Enabled {
		asc = fmt.Sprintf(`
		<tt:AudioSourceConfiguration token="ASC_%s">
			<tt:Name>AudioSourceConfig_%s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:SourceToken>AudioSource_1</tt:SourceToken>
		</tt:AudioSourceConfiguration>`,
			p.Token, p.Token,
		)
	}

	// Video Encoder Configuration
	vec := fmt.Sprintf(`
		<tt:VideoEncoderConfiguration token="VEC_%s">
			<tt:Name>VideoEncoderConfig_%s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:Encoding>%s</tt:Encoding>
			<tt:Resolution>
				<tt:Width>%d</tt:Width>
				<tt:Height>%d</tt:Height>
			</tt:Resolution>
			<tt:Quality>%.1f</tt:Quality>
			<tt:RateControl>
				<tt:FrameRateLimit>%d</tt:FrameRateLimit>
				<tt:EncodingInterval>1</tt:EncodingInterval>
				<tt:BitrateLimit>%d</tt:BitrateLimit>
			</tt:RateControl>
			<tt:H264>
				<tt:GovLength>%d</tt:GovLength>
				<tt:H264Profile>High</tt:H264Profile>
			</tt:H264>
			<tt:Multicast>
				<tt:Address>
					<tt:Type>IPv4</tt:Type>
					<tt:IPv4Address>0.0.0.0</tt:IPv4Address>
				</tt:Address>
				<tt:Port>0</tt:Port>
				<tt:TTL>1</tt:TTL>
				<tt:AutoStart>false</tt:AutoStart>
			</tt:Multicast>
			<tt:SessionTimeout>PT60S</tt:SessionTimeout>
		</tt:VideoEncoderConfiguration>`,
		p.Token, p.Token,
		p.Video.Codec,
		p.Video.Resolution.Width, p.Video.Resolution.Height,
		p.Video.Quality,
		p.Video.Framerate,
		p.Video.BitrateLimitKbps,
		p.Video.GopSize,
	)

	// Audio Encoder Configuration
	var aec string
	if p.Audio.Enabled {
		aec = fmt.Sprintf(`
		<tt:AudioEncoderConfiguration token="AEC_%s">
			<tt:Name>AudioEncoderConfig_%s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:Encoding>%s</tt:Encoding>
			<tt:Bitrate>%d</tt:Bitrate>
			<tt:SampleRate>%d</tt:SampleRate>
			<tt:Multicast>
				<tt:Address>
					<tt:Type>IPv4</tt:Type>
					<tt:IPv4Address>0.0.0.0</tt:IPv4Address>
				</tt:Address>
				<tt:Port>0</tt:Port>
				<tt:TTL>1</tt:TTL>
				<tt:AutoStart>false</tt:AutoStart>
			</tt:Multicast>
			<tt:SessionTimeout>PT60S</tt:SessionTimeout>
		</tt:AudioEncoderConfiguration>`,
			p.Token, p.Token,
			p.Audio.Codec,
			p.Audio.BitrateKbps,
			p.Audio.SampleRate,
		)
	}

	// PTZ Configuration
	var ptzConfig string
	if ptz.Enabled {
		ptzConfig = fmt.Sprintf(`
		<tt:PTZConfiguration token="PTZ_%s">
			<tt:Name>PTZConfig_%s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:NodeToken>%s</tt:NodeToken>
		</tt:PTZConfiguration>`,
			p.Token, p.Token, ptz.NodeToken,
		)
	}

	buf.WriteString(fmt.Sprintf(`
		<trt:Profiles token="%s" fixed="true">
			<tt:Name>%s</tt:Name>
			%s
			%s
			%s
			%s
			%s
		</trt:Profiles>`,
		p.Token, p.Name,
		vsc, asc, vec, aec, ptzConfig,
	))

	return buf.String()
}

// HandleGetStreamUri generates response for GetStreamUri.
func (h *MediaHandler) HandleGetStreamUri(body []byte, host string) string {
	token := extractTagValue(body, "ProfileToken")
	cfg := h.cfgMgr.Get()
	if token == "" && len(cfg.Profiles) > 0 {
		token = cfg.Profiles[0].Token
	}

	rtspPort := cfg.Server.RTSPPort
	streamURI := fmt.Sprintf("rtsp://%s:%d/live/%s", host, rtspPort, token)

	return fmt.Sprintf(`
		<trt:GetStreamUriResponse xmlns:trt="%s" xmlns:tt="%s">
			<trt:MediaUri>
				<tt:Uri>%s</tt:Uri>
				<tt:InvalidAfterConnect>false</tt:InvalidAfterConnect>
				<tt:InvalidAfterReboot>true</tt:InvalidAfterReboot>
				<tt:Timeout>PT60S</tt:Timeout>
			</trt:MediaUri>
		</trt:GetStreamUriResponse>`,
		NamespaceMediaWSDL,
		NamespaceONVIFSchema,
		streamURI,
	)
}

// HandleGetSnapshotUri generates response for GetSnapshotUri.
func (h *MediaHandler) HandleGetSnapshotUri(body []byte, host string) string {
	token := extractTagValue(body, "ProfileToken")
	cfg := h.cfgMgr.Get()
	if token == "" && len(cfg.Profiles) > 0 {
		token = cfg.Profiles[0].Token
	}

	httpPort := cfg.Server.HTTPPort
	snapshotURI := fmt.Sprintf("http://%s:%d/api/snapshot/%s", host, httpPort, token)

	return fmt.Sprintf(`
		<trt:GetSnapshotUriResponse xmlns:trt="%s" xmlns:tt="%s">
			<trt:MediaUri>
				<tt:Uri>%s</tt:Uri>
				<tt:InvalidAfterConnect>false</tt:InvalidAfterConnect>
				<tt:InvalidAfterReboot>true</tt:InvalidAfterReboot>
				<tt:Timeout>PT60S</tt:Timeout>
			</trt:MediaUri>
		</trt:GetSnapshotUriResponse>`,
		NamespaceMediaWSDL,
		NamespaceONVIFSchema,
		snapshotURI,
	)
}

// HandleGetVideoEncoderConfigurations returns encoder configurations.
func (h *MediaHandler) HandleGetVideoEncoderConfigurations() string {
	cfg := h.cfgMgr.Get()
	var configs strings.Builder

	for _, p := range cfg.Profiles {
		configs.WriteString(fmt.Sprintf(`
			<trt:Configurations token="VEC_%s">
				<tt:Name>VideoEncoderConfig_%s</tt:Name>
				<tt:UseCount>1</tt:UseCount>
				<tt:Encoding>%s</tt:Encoding>
				<tt:Resolution>
					<tt:Width>%d</tt:Width>
					<tt:Height>%d</tt:Height>
				</tt:Resolution>
				<tt:Quality>%.1f</tt:Quality>
				<tt:RateControl>
					<tt:FrameRateLimit>%d</tt:FrameRateLimit>
					<tt:EncodingInterval>1</tt:EncodingInterval>
					<tt:BitrateLimit>%d</tt:BitrateLimit>
				</tt:RateControl>
				<tt:H264>
					<tt:GovLength>%d</tt:GovLength>
					<tt:H264Profile>High</tt:H264Profile>
				</tt:H264>
				<tt:Multicast>
					<tt:Address>
						<tt:Type>IPv4</tt:Type>
						<tt:IPv4Address>0.0.0.0</tt:IPv4Address>
					</tt:Address>
					<tt:Port>0</tt:Port>
					<tt:TTL>1</tt:TTL>
					<tt:AutoStart>false</tt:AutoStart>
				</tt:Multicast>
				<tt:SessionTimeout>PT60S</tt:SessionTimeout>
			</trt:Configurations>`,
			p.Token, p.Token,
			p.Video.Codec,
			p.Video.Resolution.Width, p.Video.Resolution.Height,
			p.Video.Quality,
			p.Video.Framerate,
			p.Video.BitrateLimitKbps,
			p.Video.GopSize,
		))
	}

	return fmt.Sprintf(`
		<trt:GetVideoEncoderConfigurationsResponse xmlns:trt="%s" xmlns:tt="%s">
			%s
		</trt:GetVideoEncoderConfigurationsResponse>`,
		NamespaceMediaWSDL,
		NamespaceONVIFSchema,
		configs.String(),
	)
}

// HandleGetVideoSources returns the video sources.
func (h *MediaHandler) HandleGetVideoSources() string {
	cfg := h.cfgMgr.Get()
	width := 1920
	height := 1080
	framerate := 30
	if len(cfg.Profiles) > 0 {
		width = cfg.Profiles[0].Video.Resolution.Width
		height = cfg.Profiles[0].Video.Resolution.Height
		framerate = cfg.Profiles[0].Video.Framerate
	}

	return fmt.Sprintf(`
		<trt:GetVideoSourcesResponse xmlns:trt="%s" xmlns:tt="%s">
			<trt:VideoSources token="VideoSource_1">
				<tt:Framerate>%d</tt:Framerate>
				<tt:Resolution>
					<tt:Width>%d</tt:Width>
					<tt:Height>%d</tt:Height>
				</tt:Resolution>
			</trt:VideoSources>
		</trt:GetVideoSourcesResponse>`,
		NamespaceMediaWSDL,
		NamespaceONVIFSchema,
		framerate, width, height,
	)
}

func extractTagValue(xmlData []byte, tagName string) string {
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))
	for {
		t, err := decoder.Token()
		if err != nil {
			break
		}
		if se, ok := t.(xml.StartElement); ok {
			if se.Name.Local == tagName {
				var val string
				_ = decoder.DecodeElement(&val, &se)
				return val
			}
		}
	}
	return ""
}
