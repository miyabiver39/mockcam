package onvif

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"

	"mockcam/internal/config"
)

// Token conventions shared by the profile and configuration responses.
const (
	videoSourceToken = "VideoSource_1"
	audioSourceToken = "AudioSource_1"
)

// MediaHandler handles ONVIF Media service SOAP requests.
type MediaHandler struct {
	cfgMgr *config.Manager
}

// NewMediaHandler creates a new MediaHandler.
func NewMediaHandler(cfgMgr *config.Manager) *MediaHandler {
	return &MediaHandler{cfgMgr: cfgMgr}
}

// lookupProfile resolves the ProfileToken of a request. An empty token falls
// back to the first profile (lenient for hand-written clients); an unknown
// token yields a ter:NoProfile fault as required by the specification.
func (h *MediaHandler) lookupProfile(body []byte, cfg config.Config) (config.ProfileConfig, error) {
	token := extractTagValue(body, "ProfileToken")
	if token == "" && len(cfg.Profiles) > 0 {
		return cfg.Profiles[0], nil
	}
	for _, p := range cfg.Profiles {
		if p.Token == token {
			return p, nil
		}
	}
	return config.ProfileConfig{}, senderFault("ter:InvalidArgVal/ter:NoProfile", "The requested profile token does not exist: "+token)
}

// profileByConfigToken resolves a "<prefix><profile token>" configuration
// token (VEC_x, VSC_x, AEC_x, ASC_x) to its profile.
func (h *MediaHandler) profileByConfigToken(body []byte, prefix string, cfg config.Config) (config.ProfileConfig, error) {
	token := extractTagValue(body, "ConfigurationToken")
	for _, p := range cfg.Profiles {
		if prefix+p.Token == token {
			return p, nil
		}
	}
	return config.ProfileConfig{}, senderFault("ter:InvalidArgVal/ter:NoConfig", "The requested configuration token does not exist: "+token)
}

// onvifVideoEncoding maps a MockCam codec name to the tt:VideoEncoding value
// (Media1 knows JPEG / MPEG4 / H264; newer codecs are passed through as-is).
func onvifVideoEncoding(codec string) string {
	switch strings.ToUpper(codec) {
	case "", "H264":
		return "H264"
	case "HEVC", "H265":
		return "H265"
	case "MJPEG", "JPEG":
		return "JPEG"
	default:
		return strings.ToUpper(codec)
	}
}

// onvifAudioEncoding maps a MockCam audio codec to tt:AudioEncoding (G711 / G726 / AAC).
func onvifAudioEncoding(codec string) string {
	switch strings.ToUpper(codec) {
	case "G711A", "PCMA", "ALAW", "G711U", "PCMU", "MULAW", "G711":
		return "G711"
	case "G726", "ADPCM_G726":
		return "G726"
	default:
		return "AAC"
	}
}

// HandleGetProfiles generates response for GetProfiles.
func (h *MediaHandler) HandleGetProfiles() string {
	cfg := h.cfgMgr.Get()
	var profilesXML strings.Builder
	for _, p := range cfg.Profiles {
		profilesXML.WriteString(h.renderProfileXML("trt:Profiles", p, cfg))
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
func (h *MediaHandler) HandleGetProfile(body []byte) (string, error) {
	cfg := h.cfgMgr.Get()
	prof, err := h.lookupProfile(body, cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`
		<trt:GetProfileResponse xmlns:trt="%s" xmlns:tt="%s">
			%s
		</trt:GetProfileResponse>`,
		NamespaceMediaWSDL,
		NamespaceONVIFSchema,
		h.renderProfileXML("trt:Profile", prof, cfg),
	), nil
}

func renderVideoSourceConfigurationXML(elem string, p config.ProfileConfig) string {
	return fmt.Sprintf(`
		<%[1]s token="VSC_%[2]s">
			<tt:Name>VideoSourceConfig_%[2]s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:SourceToken>%[3]s</tt:SourceToken>
			<tt:Bounds x="0" y="0" width="%[4]d" height="%[5]d"/>
		</%[1]s>`,
		elem, p.Token, videoSourceToken, p.Video.Resolution.Width, p.Video.Resolution.Height,
	)
}

func renderAudioSourceConfigurationXML(elem string, p config.ProfileConfig) string {
	return fmt.Sprintf(`
		<%[1]s token="ASC_%[2]s">
			<tt:Name>AudioSourceConfig_%[2]s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:SourceToken>%[3]s</tt:SourceToken>
		</%[1]s>`,
		elem, p.Token, audioSourceToken,
	)
}

func renderVideoEncoderConfigurationXML(elem string, p config.ProfileConfig) string {
	encoding := onvifVideoEncoding(p.Video.Codec)
	codecDetail := ""
	if encoding == "H264" {
		codecDetail = fmt.Sprintf(`
			<tt:H264>
				<tt:GovLength>%d</tt:GovLength>
				<tt:H264Profile>High</tt:H264Profile>
			</tt:H264>`, p.Video.GopSize)
	}
	return fmt.Sprintf(`
		<%[1]s token="VEC_%[2]s">
			<tt:Name>VideoEncoderConfig_%[2]s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:Encoding>%[3]s</tt:Encoding>
			<tt:Resolution>
				<tt:Width>%[4]d</tt:Width>
				<tt:Height>%[5]d</tt:Height>
			</tt:Resolution>
			<tt:Quality>%.1[6]f</tt:Quality>
			<tt:RateControl>
				<tt:FrameRateLimit>%[7]d</tt:FrameRateLimit>
				<tt:EncodingInterval>1</tt:EncodingInterval>
				<tt:BitrateLimit>%[8]d</tt:BitrateLimit>
			</tt:RateControl>%[9]s
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
		</%[1]s>`,
		elem, p.Token,
		encoding,
		p.Video.Resolution.Width, p.Video.Resolution.Height,
		p.Video.Quality,
		p.Video.Framerate,
		p.Video.BitrateLimitKbps,
		codecDetail,
	)
}

func renderAudioEncoderConfigurationXML(elem string, p config.ProfileConfig) string {
	return fmt.Sprintf(`
		<%[1]s token="AEC_%[2]s">
			<tt:Name>AudioEncoderConfig_%[2]s</tt:Name>
			<tt:UseCount>1</tt:UseCount>
			<tt:Encoding>%[3]s</tt:Encoding>
			<tt:Bitrate>%[4]d</tt:Bitrate>
			<tt:SampleRate>%[5]d</tt:SampleRate>
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
		</%[1]s>`,
		elem, p.Token,
		onvifAudioEncoding(p.Audio.Codec),
		p.Audio.BitrateKbps,
		p.Audio.SampleRate,
	)
}

// renderProfileXML renders one tt:Profile with the element name elem
// (trt:Profiles in GetProfiles, trt:Profile in GetProfile).
func (h *MediaHandler) renderProfileXML(elem string, p config.ProfileConfig, cfg config.Config) string {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf(`
		<%s token="%s" fixed="true">
			<tt:Name>%s</tt:Name>`, elem, p.Token, xmlEscape(p.Name)))
	buf.WriteString(renderVideoSourceConfigurationXML("tt:VideoSourceConfiguration", p))
	if p.Audio.Enabled {
		buf.WriteString(renderAudioSourceConfigurationXML("tt:AudioSourceConfiguration", p))
	}
	buf.WriteString(renderVideoEncoderConfigurationXML("tt:VideoEncoderConfiguration", p))
	if p.Audio.Enabled {
		buf.WriteString(renderAudioEncoderConfigurationXML("tt:AudioEncoderConfiguration", p))
	}
	if cfg.PTZ.Enabled {
		buf.WriteString(renderPTZConfigurationXML("tt:PTZConfiguration", cfg.PTZ.NodeToken, len(cfg.Profiles)))
	}
	buf.WriteString(fmt.Sprintf(`
		</%s>`, elem))
	return buf.String()
}

func renderMediaURIResponse(action, uri string) string {
	return fmt.Sprintf(`
		<trt:%[1]sResponse xmlns:trt="%[2]s" xmlns:tt="%[3]s">
			<trt:MediaUri>
				<tt:Uri>%[4]s</tt:Uri>
				<tt:InvalidAfterConnect>false</tt:InvalidAfterConnect>
				<tt:InvalidAfterReboot>true</tt:InvalidAfterReboot>
				<tt:Timeout>PT60S</tt:Timeout>
			</trt:MediaUri>
		</trt:%[1]sResponse>`,
		action, NamespaceMediaWSDL, NamespaceONVIFSchema, uri,
	)
}

// HandleGetStreamUri generates response for GetStreamUri.
func (h *MediaHandler) HandleGetStreamUri(body []byte, host string) (string, error) {
	cfg := h.cfgMgr.Get()
	prof, err := h.lookupProfile(body, cfg)
	if err != nil {
		return "", err
	}
	return renderMediaURIResponse("GetStreamUri", fmt.Sprintf("rtsp://%s:%d/live/%s", host, cfg.Server.RTSPPort, prof.Token)), nil
}

// HandleGetSnapshotUri generates response for GetSnapshotUri.
func (h *MediaHandler) HandleGetSnapshotUri(body []byte, host string) (string, error) {
	cfg := h.cfgMgr.Get()
	prof, err := h.lookupProfile(body, cfg)
	if err != nil {
		return "", err
	}
	return renderMediaURIResponse("GetSnapshotUri", fmt.Sprintf("http://%s:%d/api/snapshot/%s", host, cfg.Server.HTTPPort, prof.Token)), nil
}

// HandleGetVideoSourceConfigurations lists one video source configuration per profile.
func (h *MediaHandler) HandleGetVideoSourceConfigurations() string {
	cfg := h.cfgMgr.Get()
	var b strings.Builder
	for _, p := range cfg.Profiles {
		b.WriteString(renderVideoSourceConfigurationXML("trt:Configurations", p))
	}
	return fmt.Sprintf(`
		<trt:GetVideoSourceConfigurationsResponse xmlns:trt="%s" xmlns:tt="%s">%s
		</trt:GetVideoSourceConfigurationsResponse>`, NamespaceMediaWSDL, NamespaceONVIFSchema, b.String())
}

// HandleGetVideoSourceConfiguration returns the configuration addressed by ConfigurationToken.
func (h *MediaHandler) HandleGetVideoSourceConfiguration(body []byte) (string, error) {
	cfg := h.cfgMgr.Get()
	p, err := h.profileByConfigToken(body, "VSC_", cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`
		<trt:GetVideoSourceConfigurationResponse xmlns:trt="%s" xmlns:tt="%s">%s
		</trt:GetVideoSourceConfigurationResponse>`, NamespaceMediaWSDL, NamespaceONVIFSchema,
		renderVideoSourceConfigurationXML("trt:Configuration", p)), nil
}

// HandleGetVideoEncoderConfigurations returns encoder configurations.
func (h *MediaHandler) HandleGetVideoEncoderConfigurations() string {
	cfg := h.cfgMgr.Get()
	var configs strings.Builder
	for _, p := range cfg.Profiles {
		configs.WriteString(renderVideoEncoderConfigurationXML("trt:Configurations", p))
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

// HandleGetVideoEncoderConfiguration returns the configuration addressed by ConfigurationToken.
func (h *MediaHandler) HandleGetVideoEncoderConfiguration(body []byte) (string, error) {
	cfg := h.cfgMgr.Get()
	p, err := h.profileByConfigToken(body, "VEC_", cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`
		<trt:GetVideoEncoderConfigurationResponse xmlns:trt="%s" xmlns:tt="%s">%s
		</trt:GetVideoEncoderConfigurationResponse>`, NamespaceMediaWSDL, NamespaceONVIFSchema,
		renderVideoEncoderConfigurationXML("trt:Configuration", p)), nil
}

// HandleGetVideoEncoderConfigurationOptions describes the encoder options.
// MockCam profiles are fixed, so the options mirror the current settings of
// the addressed profile / configuration (or the first profile when neither is given).
func (h *MediaHandler) HandleGetVideoEncoderConfigurationOptions(body []byte) (string, error) {
	cfg := h.cfgMgr.Get()
	var p config.ProfileConfig
	var err error
	if extractTagValue(body, "ConfigurationToken") != "" {
		p, err = h.profileByConfigToken(body, "VEC_", cfg)
	} else {
		p, err = h.lookupProfile(body, cfg)
	}
	if err != nil {
		return "", err
	}
	w, hgt, fps := p.Video.Resolution.Width, p.Video.Resolution.Height, p.Video.Framerate
	return fmt.Sprintf(`
		<trt:GetVideoEncoderConfigurationOptionsResponse xmlns:trt="%s" xmlns:tt="%s">
			<trt:Options>
				<tt:QualityRange><tt:Min>0</tt:Min><tt:Max>100</tt:Max></tt:QualityRange>
				<tt:H264>
					<tt:ResolutionsAvailable><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:ResolutionsAvailable>
					<tt:GovLengthRange><tt:Min>1</tt:Min><tt:Max>300</tt:Max></tt:GovLengthRange>
					<tt:FrameRateRange><tt:Min>1</tt:Min><tt:Max>%d</tt:Max></tt:FrameRateRange>
					<tt:EncodingIntervalRange><tt:Min>1</tt:Min><tt:Max>1</tt:Max></tt:EncodingIntervalRange>
					<tt:H264ProfilesSupported>High</tt:H264ProfilesSupported>
				</tt:H264>
			</trt:Options>
		</trt:GetVideoEncoderConfigurationOptionsResponse>`,
		NamespaceMediaWSDL, NamespaceONVIFSchema, w, hgt, fps), nil
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
			<trt:VideoSources token="%s">
				<tt:Framerate>%d</tt:Framerate>
				<tt:Resolution>
					<tt:Width>%d</tt:Width>
					<tt:Height>%d</tt:Height>
				</tt:Resolution>
			</trt:VideoSources>
		</trt:GetVideoSourcesResponse>`,
		NamespaceMediaWSDL,
		NamespaceONVIFSchema,
		videoSourceToken,
		framerate, width, height,
	)
}

// HandleGetAudioSources returns the audio source when any profile has audio.
func (h *MediaHandler) HandleGetAudioSources() string {
	cfg := h.cfgMgr.Get()
	sources := ""
	for _, p := range cfg.Profiles {
		if p.Audio.Enabled {
			sources = fmt.Sprintf(`
			<trt:AudioSources token="%s">
				<tt:Channels>2</tt:Channels>
			</trt:AudioSources>`, audioSourceToken)
			break
		}
	}
	return fmt.Sprintf(`
		<trt:GetAudioSourcesResponse xmlns:trt="%s" xmlns:tt="%s">%s
		</trt:GetAudioSourcesResponse>`, NamespaceMediaWSDL, NamespaceONVIFSchema, sources)
}

// HandleGetAudioSourceConfigurations lists audio source configurations of audio-enabled profiles.
func (h *MediaHandler) HandleGetAudioSourceConfigurations() string {
	cfg := h.cfgMgr.Get()
	var b strings.Builder
	for _, p := range cfg.Profiles {
		if p.Audio.Enabled {
			b.WriteString(renderAudioSourceConfigurationXML("trt:Configurations", p))
		}
	}
	return fmt.Sprintf(`
		<trt:GetAudioSourceConfigurationsResponse xmlns:trt="%s" xmlns:tt="%s">%s
		</trt:GetAudioSourceConfigurationsResponse>`, NamespaceMediaWSDL, NamespaceONVIFSchema, b.String())
}

// HandleGetAudioEncoderConfigurations lists audio encoder configurations of audio-enabled profiles.
func (h *MediaHandler) HandleGetAudioEncoderConfigurations() string {
	cfg := h.cfgMgr.Get()
	var b strings.Builder
	for _, p := range cfg.Profiles {
		if p.Audio.Enabled {
			b.WriteString(renderAudioEncoderConfigurationXML("trt:Configurations", p))
		}
	}
	return fmt.Sprintf(`
		<trt:GetAudioEncoderConfigurationsResponse xmlns:trt="%s" xmlns:tt="%s">%s
		</trt:GetAudioEncoderConfigurationsResponse>`, NamespaceMediaWSDL, NamespaceONVIFSchema, b.String())
}

// HandleGetAudioEncoderConfiguration returns the configuration addressed by ConfigurationToken.
func (h *MediaHandler) HandleGetAudioEncoderConfiguration(body []byte) (string, error) {
	cfg := h.cfgMgr.Get()
	p, err := h.profileByConfigToken(body, "AEC_", cfg)
	if err != nil {
		return "", err
	}
	if !p.Audio.Enabled {
		return "", senderFault("ter:InvalidArgVal/ter:NoConfig", "Audio is disabled for profile "+p.Token)
	}
	return fmt.Sprintf(`
		<trt:GetAudioEncoderConfigurationResponse xmlns:trt="%s" xmlns:tt="%s">%s
		</trt:GetAudioEncoderConfigurationResponse>`, NamespaceMediaWSDL, NamespaceONVIFSchema,
		renderAudioEncoderConfigurationXML("trt:Configuration", p)), nil
}

// HandleGetServiceCapabilities reports the Media service capabilities.
func (h *MediaHandler) HandleGetServiceCapabilities() string {
	return fmt.Sprintf(`
		<trt:GetServiceCapabilitiesResponse xmlns:trt="%s">
			<trt:Capabilities SnapshotUri="true" Rotation="false" VideoSourceMode="false" OSD="false">
				<trt:ProfileCapabilities MaximumNumberOfProfiles="16"/>
				<trt:StreamingCapabilities RTPMulticast="false" RTP_TCP="true" RTP_RTSP_TCP="true" NonAggregateControl="false"/>
			</trt:Capabilities>
		</trt:GetServiceCapabilitiesResponse>`, NamespaceMediaWSDL)
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
				return strings.TrimSpace(val)
			}
		}
	}
	return ""
}
