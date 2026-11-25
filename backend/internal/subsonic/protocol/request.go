package protocol

import (
	"net/http"
	"net/url"
	"strings"
)

type Request struct {
	Endpoint string
	Values   url.Values
}

func Parse(w http.ResponseWriter, r *http.Request) (Request, *Error) {
	params := Request{Endpoint: strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/rest/"), ".view")}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	err := r.ParseForm()
	params.Values = r.Form
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return params, &Error{General, "Unsupported request method"}
	}
	if err != nil {
		return params, &Error{General, "Invalid request parameters"}
	}
	switch params.Values.Get("f") {
	case "", "xml", "json":
	default:
		return params, &Error{General, "Unsupported response format"}
	}
	return params, nil
}

func (p Request) HasCredentials() bool {
	v := p.Values
	return v.Get("u") != "" && v.Get("v") != "" && v.Get("c") != "" &&
		(v.Get("p") != "" || (v.Get("t") != "" && v.Get("s") != ""))
}
