package onvif

import (
	"fmt"
	"strings"
	"time"

	"mockcam/internal/config"
)

// PTZ coordinate spaces advertised by the virtual PTZ node.
const (
	spacePanTiltPosition   = "http://www.onvif.org/ver10/tptz/PanTiltSpaces/PositionGenericSpace"
	spacePanTiltTranslate  = "http://www.onvif.org/ver10/tptz/PanTiltSpaces/TranslationGenericSpace"
	spacePanTiltVelocity   = "http://www.onvif.org/ver10/tptz/PanTiltSpaces/VelocityGenericSpace"
	spacePanTiltSpeed      = "http://www.onvif.org/ver10/tptz/PanTiltSpaces/GenericSpeedSpace"
	spaceZoomPosition      = "http://www.onvif.org/ver10/tptz/ZoomSpaces/PositionGenericSpace"
	spaceZoomTranslate     = "http://www.onvif.org/ver10/tptz/ZoomSpaces/TranslationGenericSpace"
	spaceZoomVelocity      = "http://www.onvif.org/ver10/tptz/ZoomSpaces/VelocityGenericSpace"
	spaceZoomSpeed         = "http://www.onvif.org/ver10/tptz/ZoomSpaces/ZoomGenericSpeedSpace"
	ptzConfigurationToken  = "PTZConfig_1"
	ptzMaximumPresetCount  = 32
	ptzDefaultTimeout      = "PT5S"
	ptzConfigurationName   = "PTZConfig"
	ptzNodeName            = "PTZNode_1"
	ptzTimeoutRangeMin     = "PT1S"
	ptzTimeoutRangeMax     = "PT60S"
	ptzPositionSpaceFormat = `<tt:PanTilt x="%.4f" y="%.4f" space="` + spacePanTiltPosition + `"/><tt:Zoom x="%.4f" space="` + spaceZoomPosition + `"/>`
)

// PTZHandler renders the ONVIF PTZ service (ver20/ptz) responses on top of
// the virtual PTZController.
type PTZHandler struct {
	cfgMgr *config.Manager
	ptz    *PTZController
}

// NewPTZHandler creates a PTZHandler.
func NewPTZHandler(cfgMgr *config.Manager, ptz *PTZController) *PTZHandler {
	return &PTZHandler{cfgMgr: cfgMgr, ptz: ptz}
}

// HandleGetStatus reports the current position and move state.
func (h *PTZHandler) HandleGetStatus(now time.Time) string {
	pan, tilt, zoom, moving := h.ptz.GetStatus()
	moveStatus := "IDLE"
	if moving {
		moveStatus = "MOVING"
	}
	return fmt.Sprintf(`
		<tptz:GetStatusResponse xmlns:tptz="%s" xmlns:tt="%s">
			<tptz:PTZStatus>
				<tt:Position>`+ptzPositionSpaceFormat+`</tt:Position>
				<tt:MoveStatus>
					<tt:PanTilt>%s</tt:PanTilt>
					<tt:Zoom>%s</tt:Zoom>
				</tt:MoveStatus>
				<tt:UtcTime>%s</tt:UtcTime>
			</tptz:PTZStatus>
		</tptz:GetStatusResponse>`,
		NamespacePTZWSDL, NamespaceONVIFSchema,
		pan, tilt, zoom,
		moveStatus, moveStatus,
		now.UTC().Format(time.RFC3339),
	)
}

// HandleContinuousMove starts a velocity move.
func (h *PTZHandler) HandleContinuousMove(body []byte) string {
	velX, velY, velZ := parsePTZCoords(body, "Velocity")
	h.ptz.ContinuousMove(velX, velY, velZ)
	return fmt.Sprintf(`<tptz:ContinuousMoveResponse xmlns:tptz="%s"/>`, NamespacePTZWSDL)
}

// HandleAbsoluteMove jumps to a position. Omitted PanTilt or Zoom keep their
// current value, as required by the specification.
func (h *PTZHandler) HandleAbsoluteMove(body []byte) string {
	posX, posY, posZ, hasPT, hasZoom := parsePTZVector(body, "Position")
	curPan, curTilt, curZoom, _ := h.ptz.GetStatus()
	if !hasPT {
		posX, posY = curPan, curTilt
	}
	if !hasZoom {
		posZ = curZoom
	}
	h.ptz.AbsoluteMove(posX, posY, posZ)
	return fmt.Sprintf(`<tptz:AbsoluteMoveResponse xmlns:tptz="%s"/>`, NamespacePTZWSDL)
}

// HandleRelativeMove applies a translation to the current position.
func (h *PTZHandler) HandleRelativeMove(body []byte) string {
	dX, dY, dZ := parsePTZCoords(body, "Translation")
	h.ptz.RelativeMove(dX, dY, dZ)
	return fmt.Sprintf(`<tptz:RelativeMoveResponse xmlns:tptz="%s"/>`, NamespacePTZWSDL)
}

// HandleStop halts continuous movement.
func (h *PTZHandler) HandleStop() string {
	h.ptz.Stop()
	return fmt.Sprintf(`<tptz:StopResponse xmlns:tptz="%s"/>`, NamespacePTZWSDL)
}

// HandleGotoHomePosition moves to the home position.
func (h *PTZHandler) HandleGotoHomePosition() string {
	h.ptz.GotoHome()
	return fmt.Sprintf(`<tptz:GotoHomePositionResponse xmlns:tptz="%s"/>`, NamespacePTZWSDL)
}

// HandleSetHomePosition stores the current position as home.
func (h *PTZHandler) HandleSetHomePosition() string {
	h.ptz.SetHome()
	return fmt.Sprintf(`<tptz:SetHomePositionResponse xmlns:tptz="%s"/>`, NamespacePTZWSDL)
}

// HandleGetPresets lists the persisted presets. The preset name is used as
// its token so REST/MCP and ONVIF clients address the same entries.
func (h *PTZHandler) HandleGetPresets() string {
	var b strings.Builder
	for _, p := range h.ptz.Presets() {
		name := xmlEscape(p.Name)
		fmt.Fprintf(&b, `
			<tptz:Preset token="%s">
				<tt:Name>%s</tt:Name>
				<tt:PTZPosition>`+ptzPositionSpaceFormat+`</tt:PTZPosition>
			</tptz:Preset>`, name, name, p.Pan, p.Tilt, p.Zoom)
	}
	return fmt.Sprintf(`
		<tptz:GetPresetsResponse xmlns:tptz="%s" xmlns:tt="%s">%s
		</tptz:GetPresetsResponse>`, NamespacePTZWSDL, NamespaceONVIFSchema, b.String())
}

// HandleSetPreset saves the current position. PresetToken (update) takes
// precedence over PresetName (create); with neither a name is generated.
func (h *PTZHandler) HandleSetPreset(body []byte) (string, error) {
	name := extractTagValue(body, "PresetToken")
	if name == "" {
		name = extractTagValue(body, "PresetName")
	}
	existing := h.ptz.Presets()
	isNew := true
	for _, p := range existing {
		if p.Name == strings.TrimSpace(name) && name != "" {
			isNew = false
			break
		}
	}
	if isNew && len(existing) >= ptzMaximumPresetCount {
		return "", senderFault("ter:Action/ter:TooManyPresets", "Maximum number of presets reached")
	}
	presets, err := h.ptz.SavePreset(name)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = presets[len(presets)-1].Name
	}
	return fmt.Sprintf(`
		<tptz:SetPresetResponse xmlns:tptz="%s">
			<tptz:PresetToken>%s</tptz:PresetToken>
		</tptz:SetPresetResponse>`, NamespacePTZWSDL, xmlEscape(name)), nil
}

// HandleGotoPreset moves to the preset named by PresetToken.
func (h *PTZHandler) HandleGotoPreset(body []byte) (string, error) {
	token := extractTagValue(body, "PresetToken")
	if _, ok := h.ptz.GotoPreset(token); !ok {
		return "", senderFault("ter:InvalidArgVal/ter:NoToken", "The requested preset token does not exist")
	}
	return fmt.Sprintf(`<tptz:GotoPresetResponse xmlns:tptz="%s"/>`, NamespacePTZWSDL), nil
}

// HandleRemovePreset deletes the preset named by PresetToken.
func (h *PTZHandler) HandleRemovePreset(body []byte) (string, error) {
	token := extractTagValue(body, "PresetToken")
	found := false
	for _, p := range h.ptz.Presets() {
		if p.Name == strings.TrimSpace(token) {
			found = true
			break
		}
	}
	if !found {
		return "", senderFault("ter:InvalidArgVal/ter:NoToken", "The requested preset token does not exist")
	}
	if _, err := h.ptz.DeletePreset(token); err != nil {
		return "", err
	}
	return fmt.Sprintf(`<tptz:RemovePresetResponse xmlns:tptz="%s"/>`, NamespacePTZWSDL), nil
}

func (h *PTZHandler) renderNodeXML(nodeToken string) string {
	return fmt.Sprintf(`
			<tptz:PTZNode token="%s" FixedHomePosition="false">
				<tt:Name>%s</tt:Name>
				<tt:SupportedPTZSpaces>
					<tt:AbsolutePanTiltPositionSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
						<tt:YRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:YRange>
					</tt:AbsolutePanTiltPositionSpace>
					<tt:AbsoluteZoomPositionSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>0.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:AbsoluteZoomPositionSpace>
					<tt:RelativePanTiltTranslationSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
						<tt:YRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:YRange>
					</tt:RelativePanTiltTranslationSpace>
					<tt:RelativeZoomTranslationSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:RelativeZoomTranslationSpace>
					<tt:ContinuousPanTiltVelocitySpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
						<tt:YRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:YRange>
					</tt:ContinuousPanTiltVelocitySpace>
					<tt:ContinuousZoomVelocitySpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:ContinuousZoomVelocitySpace>
					<tt:PanTiltSpeedSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>0.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:PanTiltSpeedSpace>
					<tt:ZoomSpeedSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>0.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:ZoomSpeedSpace>
				</tt:SupportedPTZSpaces>
				<tt:MaximumNumberOfPresets>%d</tt:MaximumNumberOfPresets>
				<tt:HomeSupported>true</tt:HomeSupported>
			</tptz:PTZNode>`,
		xmlEscape(nodeToken), ptzNodeName,
		spacePanTiltPosition, spaceZoomPosition,
		spacePanTiltTranslate, spaceZoomTranslate,
		spacePanTiltVelocity, spaceZoomVelocity,
		spacePanTiltSpeed, spaceZoomSpeed,
		ptzMaximumPresetCount,
	)
}

// HandleGetNodes lists the single virtual PTZ node.
func (h *PTZHandler) HandleGetNodes() string {
	cfg := h.cfgMgr.Get()
	return fmt.Sprintf(`
		<tptz:GetNodesResponse xmlns:tptz="%s" xmlns:tt="%s">%s
		</tptz:GetNodesResponse>`, NamespacePTZWSDL, NamespaceONVIFSchema, h.renderNodeXML(cfg.PTZ.NodeToken))
}

// HandleGetNode returns the node addressed by NodeToken.
func (h *PTZHandler) HandleGetNode(body []byte) (string, error) {
	cfg := h.cfgMgr.Get()
	if tok := extractTagValue(body, "NodeToken"); tok != cfg.PTZ.NodeToken {
		return "", senderFault("ter:InvalidArgVal/ter:NoEntity", "No such PTZ node: "+tok)
	}
	return fmt.Sprintf(`
		<tptz:GetNodeResponse xmlns:tptz="%s" xmlns:tt="%s">%s
		</tptz:GetNodeResponse>`, NamespacePTZWSDL, NamespaceONVIFSchema, h.renderNodeXML(cfg.PTZ.NodeToken)), nil
}

// renderPTZConfigurationXML renders the shared PTZ configuration with the
// given element name (tptz:PTZConfiguration or tt:PTZConfiguration).
func renderPTZConfigurationXML(elem, nodeToken string, useCount int) string {
	return fmt.Sprintf(`
		<%[1]s token="%[2]s">
			<tt:Name>%[3]s</tt:Name>
			<tt:UseCount>%[4]d</tt:UseCount>
			<tt:NodeToken>%[5]s</tt:NodeToken>
			<tt:DefaultAbsolutePantTiltPositionSpace>%[6]s</tt:DefaultAbsolutePantTiltPositionSpace>
			<tt:DefaultAbsoluteZoomPositionSpace>%[7]s</tt:DefaultAbsoluteZoomPositionSpace>
			<tt:DefaultRelativePanTiltTranslationSpace>%[8]s</tt:DefaultRelativePanTiltTranslationSpace>
			<tt:DefaultRelativeZoomTranslationSpace>%[9]s</tt:DefaultRelativeZoomTranslationSpace>
			<tt:DefaultContinuousPanTiltVelocitySpace>%[10]s</tt:DefaultContinuousPanTiltVelocitySpace>
			<tt:DefaultContinuousZoomVelocitySpace>%[11]s</tt:DefaultContinuousZoomVelocitySpace>
			<tt:DefaultPTZSpeed>
				<tt:PanTilt x="1.0" y="1.0" space="%[12]s"/>
				<tt:Zoom x="1.0" space="%[13]s"/>
			</tt:DefaultPTZSpeed>
			<tt:DefaultPTZTimeout>%[14]s</tt:DefaultPTZTimeout>
			<tt:PanTiltLimits>
				<tt:Range>
					<tt:URI>%[6]s</tt:URI>
					<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					<tt:YRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:YRange>
				</tt:Range>
			</tt:PanTiltLimits>
			<tt:ZoomLimits>
				<tt:Range>
					<tt:URI>%[7]s</tt:URI>
					<tt:XRange><tt:Min>0.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
				</tt:Range>
			</tt:ZoomLimits>
		</%[1]s>`,
		elem, ptzConfigurationToken, ptzConfigurationName, useCount, xmlEscape(nodeToken),
		spacePanTiltPosition, spaceZoomPosition,
		spacePanTiltTranslate, spaceZoomTranslate,
		spacePanTiltVelocity, spaceZoomVelocity,
		spacePanTiltSpeed, spaceZoomSpeed,
		ptzDefaultTimeout,
	)
}

// HandleGetConfigurations lists the shared PTZ configuration.
func (h *PTZHandler) HandleGetConfigurations() string {
	cfg := h.cfgMgr.Get()
	return fmt.Sprintf(`
		<tptz:GetConfigurationsResponse xmlns:tptz="%s" xmlns:tt="%s">%s
		</tptz:GetConfigurationsResponse>`,
		NamespacePTZWSDL, NamespaceONVIFSchema,
		renderPTZConfigurationXML("tptz:PTZConfiguration", cfg.PTZ.NodeToken, len(cfg.Profiles)))
}

// HandleGetConfiguration returns the configuration addressed by PTZConfigurationToken.
func (h *PTZHandler) HandleGetConfiguration(body []byte) (string, error) {
	if tok := extractTagValue(body, "PTZConfigurationToken"); tok != ptzConfigurationToken {
		return "", senderFault("ter:InvalidArgVal/ter:NoConfig", "No such PTZ configuration: "+tok)
	}
	cfg := h.cfgMgr.Get()
	return fmt.Sprintf(`
		<tptz:GetConfigurationResponse xmlns:tptz="%s" xmlns:tt="%s">%s
		</tptz:GetConfigurationResponse>`,
		NamespacePTZWSDL, NamespaceONVIFSchema,
		renderPTZConfigurationXML("tptz:PTZConfiguration", cfg.PTZ.NodeToken, len(cfg.Profiles))), nil
}

// HandleGetConfigurationOptions describes the supported spaces and timeouts.
func (h *PTZHandler) HandleGetConfigurationOptions(body []byte) (string, error) {
	if tok := extractTagValue(body, "ConfigurationToken"); tok != ptzConfigurationToken {
		return "", senderFault("ter:InvalidArgVal/ter:NoConfig", "No such PTZ configuration: "+tok)
	}
	return fmt.Sprintf(`
		<tptz:GetConfigurationOptionsResponse xmlns:tptz="%s" xmlns:tt="%s">
			<tptz:PTZConfigurationOptions>
				<tt:Spaces>
					<tt:AbsolutePanTiltPositionSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
						<tt:YRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:YRange>
					</tt:AbsolutePanTiltPositionSpace>
					<tt:AbsoluteZoomPositionSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>0.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:AbsoluteZoomPositionSpace>
					<tt:RelativePanTiltTranslationSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
						<tt:YRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:YRange>
					</tt:RelativePanTiltTranslationSpace>
					<tt:RelativeZoomTranslationSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:RelativeZoomTranslationSpace>
					<tt:ContinuousPanTiltVelocitySpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
						<tt:YRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:YRange>
					</tt:ContinuousPanTiltVelocitySpace>
					<tt:ContinuousZoomVelocitySpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>-1.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:ContinuousZoomVelocitySpace>
					<tt:PanTiltSpeedSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>0.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:PanTiltSpeedSpace>
					<tt:ZoomSpeedSpace>
						<tt:URI>%s</tt:URI>
						<tt:XRange><tt:Min>0.0</tt:Min><tt:Max>1.0</tt:Max></tt:XRange>
					</tt:ZoomSpeedSpace>
				</tt:Spaces>
				<tt:PTZTimeout>
					<tt:Min>%s</tt:Min>
					<tt:Max>%s</tt:Max>
				</tt:PTZTimeout>
			</tptz:PTZConfigurationOptions>
		</tptz:GetConfigurationOptionsResponse>`,
		NamespacePTZWSDL, NamespaceONVIFSchema,
		spacePanTiltPosition, spaceZoomPosition,
		spacePanTiltTranslate, spaceZoomTranslate,
		spacePanTiltVelocity, spaceZoomVelocity,
		spacePanTiltSpeed, spaceZoomSpeed,
		ptzTimeoutRangeMin, ptzTimeoutRangeMax,
	), nil
}

// HandleGetServiceCapabilities reports the PTZ service capabilities.
func (h *PTZHandler) HandleGetServiceCapabilities() string {
	return fmt.Sprintf(`
		<tptz:GetServiceCapabilitiesResponse xmlns:tptz="%s">
			<tptz:Capabilities EFlip="false" Reverse="false" GetCompatibleConfigurations="false" MoveStatus="true" StatusPosition="true"/>
		</tptz:GetServiceCapabilitiesResponse>`, NamespacePTZWSDL)
}
