package fakerenderer

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

const (
	deviceType    = "urn:schemas-upnp-org:device:MediaRenderer:1"
	servicePrefix = "urn:schemas-upnp-org:service:"
)

type actionSpec struct {
	name          string
	input, output string
}

var services = map[string][]actionSpec{
	"AVTransport": {
		{"SetAVTransportURI", "InstanceID CurrentURI CurrentURIMetaData", ""},
		{"Play", "InstanceID Speed", ""},
		{"Pause", "InstanceID", ""},
		{"Stop", "InstanceID", ""},
		{"GetTransportInfo", "InstanceID", "CurrentTransportState CurrentTransportStatus CurrentSpeed"},
		{"GetPositionInfo", "InstanceID", "Track TrackDuration TrackMetaData TrackURI RelTime AbsTime RelCount AbsCount"},
	},
	"RenderingControl": {
		{"GetVolume", "InstanceID Channel", "CurrentVolume"},
		{"SetVolume", "InstanceID Channel DesiredVolume", ""},
	},
	"ConnectionManager": {{"GetProtocolInfo", "", "Source Sink"}},
}

func (r *Renderer) description(w http.ResponseWriter, _ *http.Request) {
	s := r.State()
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	fmt.Fprintf(w, `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><device><deviceType>%s</deviceType><friendlyName>`, deviceType)
	xml.EscapeText(w, []byte(s.Name))
	fmt.Fprint(w, "</friendlyName><manufacturer>ROOMusic</manufacturer><modelName>Fake Renderer</modelName><UDN>")
	xml.EscapeText(w, []byte(s.UDN))
	fmt.Fprint(w, "</UDN><serviceList>")
	for _, name := range []string{"AVTransport", "RenderingControl", "ConnectionManager"} {
		fmt.Fprintf(w, `<service><serviceType>%[1]s%[2]s:1</serviceType><serviceId>urn:upnp-org:serviceId:%[2]s</serviceId><SCPDURL>/scpd/%[2]s</SCPDURL><controlURL>/control/%[2]s</controlURL><eventSubURL></eventSubURL></service>`, servicePrefix, name)
	}
	fmt.Fprint(w, "</serviceList></device></root>")
}

func variable(name string) (string, string) {
	switch name {
	case "InstanceID":
		return "A_ARG_TYPE_InstanceID", "ui4"
	case "Channel":
		return "A_ARG_TYPE_Channel", "string"
	case "DesiredVolume", "CurrentVolume":
		return "Volume", "ui2"
	case "Track":
		return "CurrentTrack", "ui4"
	case "RelCount", "AbsCount":
		return name, "i4"
	default:
		return name, "string"
	}
}

func (r *Renderer) scpd(w http.ResponseWriter, req *http.Request) {
	actions, ok := services[req.PathValue("service")]
	if !ok {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0"?><scpd xmlns="urn:schemas-upnp-org:service-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><actionList>`)
	variables := map[string]string{}
	for _, a := range actions {
		fmt.Fprintf(w, "<action><name>%s</name><argumentList>", a.name)
		for _, part := range []struct{ direction, names string }{{"in", a.input}, {"out", a.output}} {
			for _, name := range strings.Fields(part.names) {
				v, t := variable(name)
				variables[v] = t
				fmt.Fprintf(w, "<argument><name>%s</name><direction>%s</direction><relatedStateVariable>%s</relatedStateVariable></argument>", name, part.direction, v)
			}
		}
		fmt.Fprint(w, "</argumentList></action>")
	}
	fmt.Fprint(w, "</actionList><serviceStateTable>")
	names := make([]string, 0, len(variables))
	for name := range variables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, v := range names {
		fmt.Fprintf(w, `<stateVariable sendEvents="no"><name>%s</name><dataType>%s</dataType>`, v, variables[v])
		if v == "Volume" {
			fmt.Fprint(w, "<allowedValueRange><minimum>0</minimum><maximum>100</maximum><step>1</step></allowedValueRange>")
		}
		fmt.Fprint(w, "</stateVariable>")
	}
	fmt.Fprint(w, "</serviceStateTable></scpd>")
}
