package upnp

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

const (
	transportService  = "AVTransport"
	renderingService  = "RenderingControl"
	connectionService = "ConnectionManager"
)

type Capabilities struct {
	Pause          bool     `json:"pause"`
	Volume         string   `json:"volume"`
	StreamFormats  []string `json:"stream_formats"`
	FormatDeclared bool     `json:"format_declared"`
	MaxSampleRate  int      `json:"max_sample_rate"`
	MaxBitDepth    int      `json:"max_bit_depth"`
}

type Device struct {
	UDN          string       `json:"udn"`
	Name         string       `json:"name"`
	Model        string       `json:"model"`
	Address      string       `json:"address"`
	LocalAddress string       `json:"-"`
	Capabilities Capabilities `json:"capabilities"`

	discoveryContext context.Context
	peerIP           netip.Addr
	services         map[string]serviceEndpoint
	volumeRange      volumeRange
}

type serviceEndpoint struct {
	urn     string
	control *url.URL
	scpd    *url.URL
	actions []string
}

type describedService struct {
	Type    string `xml:"serviceType"`
	Control string `xml:"controlURL"`
	SCPD    string `xml:"SCPDURL"`
}

type describedDevice struct {
	UDN      string             `xml:"UDN"`
	Name     string             `xml:"friendlyName"`
	Model    string             `xml:"modelName"`
	Type     string             `xml:"deviceType"`
	Services []describedService `xml:"serviceList>service"`
	Children []describedDevice  `xml:"deviceList>device"`
}

func upnpType(urn, class string) string {
	prefix := "urn:schemas-upnp-org:" + class + ":"
	suffix, ok := strings.CutPrefix(strings.TrimSpace(urn), prefix)
	if !ok {
		return ""
	}
	name, version, ok := strings.Cut(suffix, ":")
	if !ok {
		return ""
	}
	n, err := strconv.Atoi(version)
	if err != nil || n < 1 {
		return ""
	}
	return name
}

func (d *describedDevice) renderer() *describedDevice {
	if upnpType(d.Type, "device") == "MediaRenderer" {
		return d
	}
	for i := range d.Children {
		if child := d.Children[i].renderer(); child != nil {
			return child
		}
	}
	return nil
}

func DiscoverDescription(ctx context.Context, location string, source netip.Addr) (Device, error) {
	u, err := deviceURL(location, nil, source)
	if err != nil {
		return Device{}, err
	}
	return describeDevice(ctx, u, source.Unmap())
}

func ManualDescription(ctx context.Context, raw string) (Device, error) {
	u, err := parseDeviceURL(raw, nil)
	if err != nil {
		return Device{}, err
	}
	ip, err := resolveDevice(ctx, u.Hostname())
	if err != nil {
		return Device{}, err
	}
	return describeDevice(ctx, u, ip)
}

func describeDevice(ctx context.Context, u *url.URL, peerIP netip.Addr) (Device, error) {
	raw, local, err := fetchDescription(ctx, u, peerIP)
	if err != nil {
		return Device{}, err
	}
	var root struct {
		XMLName xml.Name        `xml:"root"`
		URLBase string          `xml:"URLBase"`
		Device  describedDevice `xml:"device"`
	}
	if err := xml.Unmarshal(raw, &root); err != nil {
		return Device{}, fmt.Errorf("解析设备描述: %w", err)
	}
	renderer := root.Device.renderer()
	if renderer == nil || strings.TrimSpace(renderer.UDN) == "" {
		return Device{}, errors.New("设备描述缺少媒体渲染器或 UDN")
	}
	base := u
	if rawBase := strings.TrimSpace(root.URLBase); rawBase != "" {
		base, err = sameDeviceURL(rawBase, nil, u.Hostname())
		if err != nil {
			return Device{}, err
		}
	}
	d := Device{
		UDN: strings.TrimSpace(renderer.UDN), Name: strings.TrimSpace(renderer.Name), Model: strings.TrimSpace(renderer.Model),
		Address: u.String(), LocalAddress: local, peerIP: peerIP, services: make(map[string]serviceEndpoint),
		Capabilities: Capabilities{Volume: "fixed", StreamFormats: []string{"flac"}, MaxSampleRate: 48000, MaxBitDepth: 24},
		volumeRange:  volumeRange{Min: 0, Max: 100, Step: 1},
	}
	for _, s := range renderer.Services {
		name := upnpType(s.Type, "service")
		if name != transportService && name != renderingService && name != connectionService {
			continue
		}
		control, err := sameDeviceURL(strings.TrimSpace(s.Control), base, u.Hostname())
		if err != nil {
			return Device{}, err
		}
		scpd, err := sameDeviceURL(strings.TrimSpace(s.SCPD), base, u.Hostname())
		if err != nil {
			return Device{}, err
		}
		d.services[name] = serviceEndpoint{urn: strings.TrimSpace(s.Type), control: control, scpd: scpd}
	}
	if _, found := d.services[transportService]; !found {
		return Device{}, errors.New("媒体渲染器缺少 AVTransport 服务")
	}
	for name, s := range d.services {
		if err := d.readSCPD(ctx, name, s); err != nil {
			return Device{}, err
		}
	}
	if _, found := d.services[connectionService]; found {
		_, sink, err := d.GetProtocolInfo(ctx)
		if err == nil {
			d.setFormats(sink)
		} else if ctx.Err() != nil {
			return Device{}, ctx.Err()
		}
	}
	return d, nil
}

type volumeRange struct {
	Min  int `xml:"minimum"`
	Max  int `xml:"maximum"`
	Step int `xml:"step"`
}

func (d *Device) readSCPD(ctx context.Context, name string, endpoint serviceEndpoint) error {
	raw, _, err := fetchDescription(ctx, endpoint.scpd, d.peerIP)
	if err != nil {
		return err
	}
	var scpd struct {
		XMLName   xml.Name `xml:"scpd"`
		Actions   []string `xml:"actionList>action>name"`
		Variables []struct {
			Name  string       `xml:"name"`
			Range *volumeRange `xml:"allowedValueRange"`
		} `xml:"serviceStateTable>stateVariable"`
	}
	if err := xml.Unmarshal(raw, &scpd); err != nil {
		return fmt.Errorf("解析 %s SCPD: %w", name, err)
	}
	for i := range scpd.Actions {
		scpd.Actions[i] = strings.TrimSpace(scpd.Actions[i])
	}
	endpoint.actions = scpd.Actions
	d.services[name] = endpoint
	switch name {
	case transportService:
		d.Capabilities.Pause = slices.Contains(endpoint.actions, "Pause")
	case renderingService:
		if slices.Contains(endpoint.actions, "SetVolume") {
			d.Capabilities.Volume = "number"
		}
		for _, v := range scpd.Variables {
			r := v.Range
			if strings.TrimSpace(v.Name) == "Volume" && r != nil && r.Min >= 0 && r.Max > r.Min && r.Max <= 65535 {
				d.volumeRange = *r
				d.volumeRange.Step = min(max(1, r.Step), r.Max-r.Min)
			}
		}
	}
	return nil
}

func (d *Device) setFormats(sink string) {
	wav := false
	for _, entry := range strings.Split(sink, ",") {
		fields := strings.SplitN(strings.ToLower(strings.TrimSpace(entry)), ":", 4)
		if len(fields) != 4 || fields[0] != "http-get" {
			continue
		}
		mime, _, _ := strings.Cut(fields[2], ";")
		switch strings.TrimSpace(mime) {
		case "audio/flac", "audio/x-flac":
			d.Capabilities.FormatDeclared = true
			return
		case "audio/wav", "audio/x-wav", "audio/l16":
			wav = true
		}
	}
	if wav {
		d.Capabilities.StreamFormats = []string{"wav"}
		d.Capabilities.FormatDeclared = true
	}
}
