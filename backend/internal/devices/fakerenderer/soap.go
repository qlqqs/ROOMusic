package fakerenderer

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

const envelopeStart = `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`
const envelopeEnd = "</s:Body></s:Envelope>"

type soapArgument struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}
type soapAction struct {
	XMLName xml.Name
	Args    []soapArgument `xml:",any"`
}

func (r *Renderer) soap(w http.ResponseWriter, req *http.Request) {
	service := req.PathValue("service")
	actions, ok := services[service]
	if !ok {
		http.NotFound(w, req)
		return
	}
	// 跳过 Envelope 头部，只接受 Body 内的动作。
	var envelope struct {
		Body struct {
			Action soapAction `xml:",any"`
		} `xml:"http://schemas.xmlsoap.org/soap/envelope/ Body"`
	}
	if err := xml.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10)).Decode(&envelope); err != nil {
		http.Error(w, "无效的 SOAP 请求", http.StatusBadRequest)
		return
	}
	action := envelope.Body.Action
	namespace := servicePrefix + service + ":1"
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	for _, spec := range actions {
		if spec.name != action.XMLName.Local || namespace != action.XMLName.Space || strings.Trim(req.Header.Get("SOAPAction"), `"`) != namespace+"#"+spec.name {
			continue
		}
		args := map[string]string{}
		for _, arg := range action.Args {
			args[arg.XMLName.Local] = arg.Value
		}
		fields, fault := r.act(spec.name, args)
		if fault != nil {
			soapFault(w, fault)
			return
		}
		fmt.Fprintf(w, `%s<u:%sResponse xmlns:u="%s">`, envelopeStart, spec.name, namespace)
		for _, key := range strings.Fields(spec.output) {
			fmt.Fprintf(w, "<%s>", key)
			xml.EscapeText(w, []byte(fields[key]))
			fmt.Fprintf(w, "</%s>", key)
		}
		fmt.Fprintf(w, "</u:%sResponse>%s", spec.name, envelopeEnd)
		return
	}
	soapFault(w, &actionError{401, "Invalid Action"})
}

func soapFault(w http.ResponseWriter, f *actionError) {
	w.WriteHeader(http.StatusInternalServerError)
	fmt.Fprintf(w, `%s<s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%d</errorCode><errorDescription>%s</errorDescription></UPnPError></detail></s:Fault>%s`, envelopeStart, f.code, f.description, envelopeEnd)
}
