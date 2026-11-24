package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

var ErrUnsupportedAction = errors.New("设备未声明该动作")

const soapEnvelope = "http://schemas.xmlsoap.org/soap/envelope/"

type soapArgument struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

type soapResult struct {
	XMLName   xml.Name
	Fields    []soapArgument `xml:",any"`
	FaultCode string         `xml:"detail>UPnPError>errorCode"`
}

func (r soapResult) field(name string) string {
	for _, f := range r.Fields {
		if f.XMLName.Local == name {
			return strings.TrimSpace(f.Value)
		}
	}
	return ""
}

func arg(name, value string) soapArgument {
	return soapArgument{XMLName: xml.Name{Local: name}, Value: value}
}

func (d Device) invoke(ctx context.Context, service, action string, args ...soapArgument) (soapResult, error) {
	if d.discoveryContext != nil {
		if d.discoveryContext.Err() != nil {
			return soapResult{}, ErrDisabled
		}
		requestCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(d.discoveryContext, cancel)
		defer stop()
		ctx = requestCtx
	}
	endpoint, found := d.services[service]
	if !found {
		return soapResult{}, ErrUnsupportedAction
	}
	actionXML, err := xml.Marshal(struct {
		XMLName   xml.Name
		Namespace string         `xml:"xmlns:u,attr"`
		Arguments []soapArgument `xml:",any"`
	}{
		XMLName: xml.Name{Local: "u:" + action}, Namespace: endpoint.urn, Arguments: args,
	})
	if err != nil {
		return soapResult{}, err
	}
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="` + soapEnvelope + `" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	body.Write(actionXML)
	body.WriteString("</s:Body></s:Envelope>")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.control.String(), &body)
	if err != nil {
		return soapResult{}, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"`+endpoint.urn+"#"+action+`"`)
	data, status, err := readResponse(req, d.peerIP, soapLimit)
	if err != nil {
		return soapResult{}, err
	}
	var response struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Result soapResult `xml:",any"`
		} `xml:"Body"`
	}
	parseErr := xml.Unmarshal(data, &response)
	result := response.Body.Result
	if result.XMLName.Local == "Fault" {
		return soapResult{}, fmt.Errorf("设备拒绝 %s（UPnP %s）", action, result.FaultCode)
	}
	if status != http.StatusOK {
		return soapResult{}, fmt.Errorf("设备动作 %s 返回 HTTP %d", action, status)
	}
	if parseErr != nil || result.XMLName.Local != action+"Response" {
		return soapResult{}, fmt.Errorf("设备动作 %s 的 SOAP 响应无效", action)
	}
	return result, nil
}

func (d Device) transport(ctx context.Context, action string, args ...soapArgument) (soapResult, error) {
	return d.invoke(ctx, transportService, action, append([]soapArgument{arg("InstanceID", "0")}, args...)...)
}

func (d Device) SetAVTransportURI(ctx context.Context, uri, metadata string) error {
	_, err := d.transport(ctx, "SetAVTransportURI", arg("CurrentURI", uri), arg("CurrentURIMetaData", metadata))
	return err
}

func (d Device) Play(ctx context.Context) error {
	_, err := d.transport(ctx, "Play", arg("Speed", "1"))
	return err
}

func (d Device) Pause(ctx context.Context) error {
	if !d.Capabilities.Pause {
		return d.Stop(ctx)
	}
	_, err := d.transport(ctx, "Pause")
	return err
}

func (d Device) Stop(ctx context.Context) error {
	_, err := d.transport(ctx, "Stop")
	return err
}

func (d Device) GetTransportInfo(ctx context.Context) (string, error) {
	r, err := d.transport(ctx, "GetTransportInfo")
	if err != nil {
		return "", err
	}
	state := r.field("CurrentTransportState")
	if state == "" {
		return "", errors.New("设备未返回播放状态")
	}
	return state, nil
}

func (d Device) GetPositionInfo(ctx context.Context) (time.Duration, error) {
	r, err := d.transport(ctx, "GetPositionInfo")
	if err != nil {
		return 0, err
	}
	value := r.field("RelTime")
	if value == "NOT_IMPLEMENTED" {
		return 0, nil
	}
	hours, rest, ok := strings.Cut(value, ":")
	minutes, seconds, ok2 := strings.Cut(rest, ":")
	if !ok || !ok2 {
		return 0, errors.New("设备未返回有效的相对时间")
	}
	return time.ParseDuration(hours + "h" + minutes + "m" + seconds + "s")
}

func (d Device) GetProtocolInfo(ctx context.Context) (source, sink string, err error) {
	r, err := d.invoke(ctx, connectionService, "GetProtocolInfo")
	return r.field("Source"), r.field("Sink"), err
}

func (d Device) GetVolume(ctx context.Context) (int, error) {
	if !slices.Contains(d.services[renderingService].actions, "GetVolume") {
		return 0, ErrUnsupportedAction
	}
	result, err := d.invoke(ctx, renderingService, "GetVolume", arg("InstanceID", "0"), arg("Channel", "Master"))
	if err != nil {
		return 0, err
	}
	volume, err := strconv.Atoi(result.field("CurrentVolume"))
	r := d.volumeRange
	if err != nil || volume < r.Min || volume > r.Max {
		return 0, errors.New("设备返回的音量无效")
	}
	span := r.Max - r.Min
	return ((volume-r.Min)*100 + span/2) / span, nil
}

func (d Device) SetVolume(ctx context.Context, percent int) error {
	if !slices.Contains(d.services[renderingService].actions, "SetVolume") {
		return ErrUnsupportedAction
	}
	if percent < 0 || percent > 100 {
		return errors.New("音量必须在 0 至 100 之间")
	}
	r := d.volumeRange
	span := r.Max - r.Min
	steps := (percent*span + 50*r.Step) / (100 * r.Step)
	volume := r.Min + min(steps, span/r.Step)*r.Step
	_, err := d.invoke(ctx, renderingService, "SetVolume", arg("InstanceID", "0"), arg("Channel", "Master"), arg("DesiredVolume", strconv.Itoa(volume)))
	return err
}
