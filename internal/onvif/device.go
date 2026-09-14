package onvif

import (
	"fmt"
	"time"

	"mockcam/internal/config"
)

// DeviceHandler handles ONVIF Device service SOAP requests.
type DeviceHandler struct {
	cfgMgr *config.Manager
}

// NewDeviceHandler creates a new DeviceHandler.
func NewDeviceHandler(cfgMgr *config.Manager) *DeviceHandler {
	return &DeviceHandler{cfgMgr: cfgMgr}
}

// HandleGetDeviceInformation generates response for GetDeviceInformation.
func (h *DeviceHandler) HandleGetDeviceInformation() string {
	cfg := h.cfgMgr.Get()
	info := cfg.Server.DeviceInfo

	return fmt.Sprintf(`
		<tds:GetDeviceInformationResponse xmlns:tds="%s">
			<tds:Manufacturer>%s</tds:Manufacturer>
			<tds:Model>%s</tds:Model>
			<tds:FirmwareVersion>%s</tds:FirmwareVersion>
			<tds:SerialNumber>%s</tds:SerialNumber>
			<tds:HardwareId>%s</tds:HardwareId>
		</tds:GetDeviceInformationResponse>`,
		NamespaceDeviceWSDL,
		info.Manufacturer,
		info.Model,
		info.FirmwareVersion,
		info.SerialNumber,
		info.HardwareID,
	)
}

// HandleGetSystemDateAndTime generates response for GetSystemDateAndTime.
func (h *DeviceHandler) HandleGetSystemDateAndTime() string {
	now := time.Now().UTC()

	return fmt.Sprintf(`
		<tds:GetSystemDateAndTimeResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:SystemDateAndTime>
				<tt:DateTimeType>Manual</tt:DateTimeType>
				<tt:DaylightSavings>false</tt:DaylightSavings>
				<tt:TimeZone>
					<tt:TZ>UTC</tt:TZ>
				</tt:TimeZone>
				<tt:UTCDateTime>
					<tt:Time>
						<tt:Hour>%02d</tt:Hour>
						<tt:Minute>%02d</tt:Minute>
						<tt:Second>%02d</tt:Second>
					</tt:Time>
					<tt:Date>
						<tt:Year>%04d</tt:Year>
						<tt:Month>%02d</tt:Month>
						<tt:Day>%02d</tt:Day>
					</tt:Date>
				</tt:UTCDateTime>
			</tds:SystemDateAndTime>
		</tds:GetSystemDateAndTimeResponse>`,
		NamespaceDeviceWSDL,
		NamespaceONVIFSchema,
		now.Hour(), now.Minute(), now.Second(),
		now.Year(), int(now.Month()), now.Day(),
	)
}

// HandleGetCapabilities generates response for GetCapabilities.
func (h *DeviceHandler) HandleGetCapabilities(host string) string {
	cfg := h.cfgMgr.Get()
	httpPort := cfg.Server.HTTPPort

	deviceXAddr := fmt.Sprintf("http://%s:%d/onvif/device_service", host, httpPort)
	mediaXAddr := fmt.Sprintf("http://%s:%d/onvif/media_service", host, httpPort)
	ptzXAddr := fmt.Sprintf("http://%s:%d/onvif/ptz_service", host, httpPort)

	return fmt.Sprintf(`
		<tds:GetCapabilitiesResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:Capabilities>
				<tt:Device>
					<tt:XAddr>%s</tt:XAddr>
					<tt:Network>
						<tt:IPFilter>false</tt:IPFilter>
						<tt:ZeroConfiguration>false</tt:ZeroConfiguration>
						<tt:IPAddressFilter>false</tt:IPAddressFilter>
						<tt:DynDNS>false</tt:DynDNS>
					</tt:Network>
					<tt:System>
						<tt:DiscoveryResolve>false</tt:DiscoveryResolve>
						<tt:DiscoveryBye>false</tt:DiscoveryBye>
						<tt:RemoteDiscovery>false</tt:RemoteDiscovery>
						<tt:SystemBackup>false</tt:SystemBackup>
						<tt:SystemLogging>false</tt:SystemLogging>
						<tt:FirmwareUpgrade>false</tt:FirmwareUpgrade>
						<tt:SupportedVersions>
							<tt:Major>2</tt:Major>
							<tt:Minor>0</tt:Minor>
						</tt:SupportedVersions>
					</tt:System>
				</tt:Device>
				<tt:Media>
					<tt:XAddr>%s</tt:XAddr>
					<tt:StreamingCapabilities>
						<tt:RTPMulticast>false</tt:RTPMulticast>
						<tt:RTP_TCP>true</tt:RTP_TCP>
						<tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP>
					</tt:StreamingCapabilities>
				</tt:Media>
				<tt:PTZ>
					<tt:XAddr>%s</tt:XAddr>
				</tt:PTZ>
			</tds:Capabilities>
		</tds:GetCapabilitiesResponse>`,
		NamespaceDeviceWSDL,
		NamespaceONVIFSchema,
		deviceXAddr,
		mediaXAddr,
		ptzXAddr,
	)
}

// HandleGetServices generates response for GetServices.
func (h *DeviceHandler) HandleGetServices(host string) string {
	cfg := h.cfgMgr.Get()
	httpPort := cfg.Server.HTTPPort

	deviceXAddr := fmt.Sprintf("http://%s:%d/onvif/device_service", host, httpPort)
	mediaXAddr := fmt.Sprintf("http://%s:%d/onvif/media_service", host, httpPort)
	ptzXAddr := fmt.Sprintf("http://%s:%d/onvif/ptz_service", host, httpPort)

	return fmt.Sprintf(`
		<tds:GetServicesResponse xmlns:tds="%s">
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tds:Major>2</tds:Major>
					<tds:Minor>0</tds:Minor>
				</tds:Version>
			</tds:Service>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tds:Major>2</tds:Major>
					<tds:Minor>0</tds:Minor>
				</tds:Version>
			</tds:Service>
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>%s</tds:XAddr>
				<tds:Version>
					<tds:Major>2</tds:Major>
					<tds:Minor>0</tds:Minor>
				</tds:Version>
			</tds:Service>
		</tds:GetServicesResponse>`,
		NamespaceDeviceWSDL,
		NamespaceDeviceWSDL, deviceXAddr,
		NamespaceMediaWSDL, mediaXAddr,
		NamespacePTZWSDL, ptzXAddr,
	)
}

// HandleGetScopes generates response for GetScopes.
func (h *DeviceHandler) HandleGetScopes() string {
	cfg := h.cfgMgr.Get()
	model := cfg.Server.DeviceInfo.Model

	return fmt.Sprintf(`
		<tds:GetScopesResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/type/NetworkVideoTransmitter</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/name/MockCam</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Fixed</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/hardware/%s</tt:ScopeItem>
			</tds:Scopes>
			<tds:Scopes>
				<tt:ScopeDef>Configurable</tt:ScopeDef>
				<tt:ScopeItem>onvif://www.onvif.org/location/any</tt:ScopeItem>
			</tds:Scopes>
		</tds:GetScopesResponse>`,
		NamespaceDeviceWSDL,
		NamespaceONVIFSchema,
		model,
	)
}

// HandleGetHostname generates response for GetHostname.
func (h *DeviceHandler) HandleGetHostname() string {
	return fmt.Sprintf(`
		<tds:GetHostnameResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:HostnameInformation>
				<tt:FromDHCP>false</tt:FromDHCP>
				<tt:Name>MockCam</tt:Name>
			</tds:HostnameInformation>
		</tds:GetHostnameResponse>`,
		NamespaceDeviceWSDL,
		NamespaceONVIFSchema,
	)
}
