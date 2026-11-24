package upnp

import (
	"encoding/xml"
	"fmt"
	"strings"
	"unicode/utf8"
)

type TrackMetadata struct {
	Title    string
	Artist   string
	Album    string
	ArtURI   string
	URI      string
	MIMEType string
}

func BuildDIDL(track TrackMetadata) string {
	type resource struct {
		Protocol string `xml:"protocolInfo,attr"`
		URI      string `xml:",chardata"`
	}
	item := struct {
		XMLName    xml.Name `xml:"item"`
		ID         string   `xml:"id,attr"`
		ParentID   string   `xml:"parentID,attr"`
		Restricted string   `xml:"restricted,attr"`
		Title      string   `xml:"dc:title"`
		Artist     string   `xml:"upnp:artist,omitempty"`
		Album      string   `xml:"upnp:album,omitempty"`
		ArtURI     string   `xml:"upnp:albumArtURI,omitempty"`
		Class      string   `xml:"upnp:class"`
		Resource   resource `xml:"res"`
	}{
		ID: "1", ParentID: "0", Restricted: "1", Title: track.Title, Artist: track.Artist, Album: track.Album, ArtURI: track.ArtURI,
		Class:    "object.item.audioItem.musicTrack",
		Resource: resource{Protocol: "http-get:*:" + track.MIMEType + ":DLNA.ORG_OP=00;DLNA.ORG_CI=1;DLNA.ORG_FLAGS=01700000000000000000000000000000", URI: track.URI},
	}
	const root = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">`
	encoded, _ := xml.Marshal(item)
	var b strings.Builder
	b.WriteString(root)
	for _, ch := range string(encoded) {
		if ch < utf8.RuneSelf {
			b.WriteByte(byte(ch))
		} else {
			fmt.Fprintf(&b, "&#%d;", ch)
		}
	}
	b.WriteString("</DIDL-Lite>")
	return b.String()
}
