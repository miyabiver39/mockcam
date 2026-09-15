package onvif

import (
	"fmt"
	"net"
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
		xmlEscape(info.Manufacturer),
		xmlEscape(info.Model),
		xmlEscape(info.FirmwareVersion),
		xmlEscape(info.SerialNumber),
		xmlEscape(info.HardwareID),
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

// HandleGetCapabilities generates response for GetCapabilities. The PTZ
// section is only advertised while PTZ is enabled in the configuration.
func (h *DeviceHandler) HandleGetCapabilities(host string) string {
	cfg := h.cfgMgr.Get()
	httpPort := cfg.Server.HTTPPort

	deviceXAddr := fmt.Sprintf("http://%s:%d/onvif/device_service", host, httpPort)
	mediaXAddr := fmt.Sprintf("http://%s:%d/onvif/media_service", host, httpPort)
	ptzCap := ""
	if cfg.PTZ.Enabled {
		ptzCap = fmt.Sprintf(`<tt:PTZ>
					<tt:XAddr>http://%s:%d/onvif/ptz_service</tt:XAddr>
				</tt:PTZ>`, host, httpPort)
	}

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
				%s
			</tds:Capabilities>
		</tds:GetCapabilitiesResponse>`,
		NamespaceDeviceWSDL,
		NamespaceONVIFSchema,
		deviceXAddr,
		mediaXAddr,
		ptzCap,
	)
}

// HandleGetServices generates response for GetServices. The PTZ service is
// only listed while PTZ is enabled in the configuration.
func (h *DeviceHandler) HandleGetServices(host string) string {
	cfg := h.cfgMgr.Get()
	httpPort := cfg.Server.HTTPPort

	service := func(ns, path string) string {
		return fmt.Sprintf(`
			<tds:Service>
				<tds:Namespace>%s</tds:Namespace>
				<tds:XAddr>http://%s:%d/onvif/%s</tds:XAddr>
				<tds:Version>
					<tds:Major>2</tds:Major>
					<tds:Minor>0</tds:Minor>
				</tds:Version>
			</tds:Service>`, ns, host, httpPort, path)
	}
	services := service(NamespaceDeviceWSDL, "device_service") + service(NamespaceMediaWSDL, "media_service")
	if cfg.PTZ.Enabled {
		services += service(NamespacePTZWSDL, "ptz_service")
	}

	return fmt.Sprintf(`
		<tds:GetServicesResponse xmlns:tds="%s">%s
		</tds:GetServicesResponse>`,
		NamespaceDeviceWSDL, services,
	)
}

// HandleGetServiceCapabilities reports the Device service capabilities.
func (h *DeviceHandler) HandleGetServiceCapabilities() string {
	return fmt.Sprintf(`
		<tds:GetServiceCapabilitiesResponse xmlns:tds="%s">
			<tds:Capabilities>
				<tds:Network IPFilter="false" ZeroConfiguration="false" IPVersion6="false" DynDNS="false" Dot11Configuration="false" HostnameFromDHCP="false" NTP="0"/>
				<tds:Security TLS1.0="false" TLS1.1="false" TLS1.2="false" OnboardKeyGeneration="false" AccessPolicyConfig="false" DefaultAccessPolicy="false" Dot1X="false" RemoteUserHandling="false" X.509Token="false" SAMLToken="false" KerberosToken="false" UsernameToken="true" HttpDigest="true" RELToken="false"/>
				<tds:System DiscoveryResolve="false" DiscoveryBye="false" RemoteDiscovery="false" SystemBackup="false" SystemLogging="false" FirmwareUpgrade="false" HttpFirmwareUpgrade="false" HttpSystemBackup="false" HttpSystemLogging="false" HttpSupportInformation="false"/>
			</tds:Capabilities>
		</tds:GetServiceCapabilitiesResponse>`, NamespaceDeviceWSDL)
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
		xmlEscape(model),
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

// HandleGetNetworkInterfaces describes a single virtual interface. The IPv4
// address is the one the client reached us on (when it is an IP literal).
func (h *DeviceHandler) HandleGetNetworkInterfaces(host string) string {
	ip := "0.0.0.0"
	if parsed := net.ParseIP(host); parsed != nil && parsed.To4() != nil {
		ip = parsed.String()
	}
	return fmt.Sprintf(`
		<tds:GetNetworkInterfacesResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:NetworkInterfaces token="eth0">
				<tt:Enabled>true</tt:Enabled>
				<tt:Info>
					<tt:Name>eth0</tt:Name>
					<tt:HwAddress>02:4d:4f:43:4b:01</tt:HwAddress>
					<tt:MTU>1500</tt:MTU>
				</tt:Info>
				<tt:IPv4>
					<tt:Enabled>true</tt:Enabled>
					<tt:Config>
						<tt:Manual>
							<tt:Address>%s</tt:Address>
							<tt:PrefixLength>24</tt:PrefixLength>
						</tt:Manual>
						<tt:DHCP>false</tt:DHCP>
					</tt:Config>
				</tt:IPv4>
			</tds:NetworkInterfaces>
		</tds:GetNetworkInterfacesResponse>`, NamespaceDeviceWSDL, NamespaceONVIFSchema, ip)
}

// HandleGetNetworkProtocols lists the HTTP and RTSP listeners.
func (h *DeviceHandler) HandleGetNetworkProtocols() string {
	cfg := h.cfgMgr.Get()
	return fmt.Sprintf(`
		<tds:GetNetworkProtocolsResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:NetworkProtocols>
				<tt:Name>HTTP</tt:Name>
				<tt:Enabled>true</tt:Enabled>
				<tt:Port>%d</tt:Port>
			</tds:NetworkProtocols>
			<tds:NetworkProtocols>
				<tt:Name>HTTPS</tt:Name>
				<tt:Enabled>false</tt:Enabled>
				<tt:Port>443</tt:Port>
			</tds:NetworkProtocols>
			<tds:NetworkProtocols>
				<tt:Name>RTSP</tt:Name>
				<tt:Enabled>true</tt:Enabled>
				<tt:Port>%d</tt:Port>
			</tds:NetworkProtocols>
		</tds:GetNetworkProtocolsResponse>`, NamespaceDeviceWSDL, NamespaceONVIFSchema, cfg.Server.HTTPPort, cfg.Server.RTSPPort)
}

// HandleGetDNS reports a static (empty) DNS configuration.
func (h *DeviceHandler) HandleGetDNS() string {
	return fmt.Sprintf(`
		<tds:GetDNSResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:DNSInformation>
				<tt:FromDHCP>false</tt:FromDHCP>
			</tds:DNSInformation>
		</tds:GetDNSResponse>`, NamespaceDeviceWSDL, NamespaceONVIFSchema)
}

// HandleGetNTP reports a static (empty) NTP configuration.
func (h *DeviceHandler) HandleGetNTP() string {
	return fmt.Sprintf(`
		<tds:GetNTPResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:NTPInformation>
				<tt:FromDHCP>false</tt:FromDHCP>
			</tds:NTPInformation>
		</tds:GetNTPResponse>`, NamespaceDeviceWSDL, NamespaceONVIFSchema)
}

// HandleGetDiscoveryMode reports that the device answers WS-Discovery probes.
func (h *DeviceHandler) HandleGetDiscoveryMode() string {
	return fmt.Sprintf(`
		<tds:GetDiscoveryModeResponse xmlns:tds="%s">
			<tds:DiscoveryMode>Discoverable</tds:DiscoveryMode>
		</tds:GetDiscoveryModeResponse>`, NamespaceDeviceWSDL)
}

// HandleGetUsers lists the configured account (the password is never returned).
func (h *DeviceHandler) HandleGetUsers() string {
	cfg := h.cfgMgr.Get()
	return fmt.Sprintf(`
		<tds:GetUsersResponse xmlns:tds="%s" xmlns:tt="%s">
			<tds:User>
				<tt:Username>%s</tt:Username>
				<tt:UserLevel>Administrator</tt:UserLevel>
			</tds:User>
		</tds:GetUsersResponse>`, NamespaceDeviceWSDL, NamespaceONVIFSchema, xmlEscape(cfg.Server.AuthUser))
}

// HandleGetWsdlUrl points at the public ONVIF WSDL location.
func (h *DeviceHandler) HandleGetWsdlUrl() string {
	return fmt.Sprintf(`
		<tds:GetWsdlUrlResponse xmlns:tds="%s">
			<tds:WsdlUrl>http://www.onvif.org/</tds:WsdlUrl>
		</tds:GetWsdlUrlResponse>`, NamespaceDeviceWSDL)
}
