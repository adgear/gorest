// Copyright (c) 2014 Datacratic. All rights reserved.

package rest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client is a convenience REST client which simplifies the creation of REST
// requests.
type Client struct {
	*http.Client

	// Host is the address of the remote REST endpoint where requests should be
	// sent to.
	Host string

	// Root is a prefix that will be preprended to all path requests created by
	// this client.
	Root string
}

// NewRequest creates a new Request object for the given HTTP method.
func (client *Client) NewRequest(method string) *Request {
	if client.Client == nil {
		client.Client = http.DefaultClient
	}

	return &Request{
		Host:   client.Host,
		Method: method,
		Root:   client.Root,
		Client: client.Client,
	}
}

// Request is used to gradually construct REST requests and send them to a
// remote endpoint. Request object should be created via the NewRequest function
// or the Client.NewRequest function and filled in via the SetXxx and AddXxx
// functions. Finally the request is sent via the Send function which returns a
// Response object.
type Request struct {

	// Client is the http.Client used to send the request. Defaults to
	// http.DefaultClient and can changed via the SetClient method.
	Client *http.Client

	// Host is the address of the remote REST endpoint where requests should be
	// sent to.
	Host string

	// Root is a prefix that will be preprended to all path requests created by
	// this client.
	Root string

	// Path is the absolute path where the Request should be routed to on the
	// remote endpoint. Can be changed via the SetPath method.
	Path string

	// Method is the HTTP verb used for the HTTP request.
	Method string

	// Header contains all the headers to be added to the HTTP request. Can be
	// changed via the AddHeader method.
	Header http.Header

	// Body is the JSON serialized body of the HTTP request. Can be set via the
	// SetBody method.
	Body []byte

	HTTP *http.Request

	err *Error
}

// NewRequest creates a new Request object to be sent to the given host using
// the given HTTP verb.
func NewRequest(host, method string) *Request {
	return &Request{
		Host:   host,
		Method: method,
		Client: http.DefaultClient,
	}
}

// SetClient selects the http.Client to be used to execute the requests.
func (req *Request) SetClient(client *http.Client) *Request {
	req.Client = client
	return req
}

// SetPath formats and sets the path where the request will be routed to. Note
// that the root is prefixed to the path before formatting the string.
func (req *Request) SetPath(path string, args ...interface{}) *Request {
	req.Path = fmt.Sprintf(JoinPath(req.Root, path), args...)
	return req
}

// AddHeader adds the given header to the request.
func (req *Request) AddHeader(key, value string) *Request {
	if req.Header == nil {
		req.Header = make(http.Header)
	}

	req.Header.Add(key, value)
	return req
}

// SetBody marshals the given objects and sets it as the body of the
// request. The Content-Length header will be automatically set.
func (req *Request) SetBody(obj interface{}) *Request {
	var err error
	if req.Body, err = json.Marshal(obj); err == nil {
		req.AddHeader("Content-Length", strconv.Itoa(len(req.Body)))

	} else {
		req.err = &Error{MarshalError, err}
	}

	return req
}

// Send attempts to send the request to the remote endpoint and returns a
// Response which contains the result.
func (req *Request) Send() *Response {
	t0 := time.Now()

	if len(req.Path) == 0 {
		req.Path = req.Root
	}

	resp := &Response{Request: req, Error: req.err}

	if resp.Error == nil {
		req.send(resp)
	}

	resp.Latency = time.Since(t0)
	return resp
}

func (req *Request) send(resp *Response) {
	var reader io.Reader
	if len(req.Body) > 0 {
		reader = bytes.NewReader(req.Body)
	}

	url := strings.TrimRight(req.Host, "/") + req.Path

	var err error

	if req.HTTP, err = http.NewRequest(req.Method, url, reader); err != nil {
		resp.Error = &Error{NewRequestError, err}
		return
	}

	req.AddHeader("Content-Type", "application/json")
	req.HTTP.Header = req.Header

	httpResp, err := req.Client.Do(req.HTTP)
	if err != nil {
		resp.Error = &Error{SendRequestError, err}
		return
	}

	resp.Code = httpResp.StatusCode
	resp.Header = httpResp.Header

	if resp.Body, err = ioutil.ReadAll(httpResp.Body); err != nil {
		resp.Error = &Error{ReadBodyError, err}
	}

	httpResp.Body.Close()
	return
}

// Response holds the result of a sent REST request. The response should be read
// via the GetBody method which checks the various fields to detect errors.
type Response struct {

	// Request is the request that originated the response.
	Request *Request

	// Code is the http status code returned by the endpoint.
	Code int

	// Header holds the headers of the HTTP response.
	Header http.Header

	// Body holds the raw unmarshalled body of the HTTP response. GetBody can be
	// used to unmarshal the body.
	Body []byte

	// Error is set if an error occured while sending the request.
	Error *Error

	// Latency indicates how long the request round-trip took.
	Latency time.Duration
}

// GetBody checks the various fields of the response for errors and unmarshals
// the response body if the given object is not nil. If an error is detected,
// the error type and error will be returned instead.
func (resp *Response) GetBody(obj interface{}) (err *Error) {
	if resp.Error != nil {
		err = resp.Error

	} else if resp.Code == http.StatusNotFound {
		err = &Error{UnknownRoute, errors.New(string(resp.Body))}

	} else if resp.Code >= 400 {
		err = &Error{EndpointError, errors.New(string(resp.Body))}

	} else if resp.Code < 200 && resp.Code >= 300 {
		err = ErrorFmt(UnexpectedStatusCode, "unexpected status code: %d", resp.Code)

	} else if resp.Code == http.StatusNoContent {
		if obj == nil {
			return
		}
		err = ErrorFmt(UnexpectedStatusCode, "unexpected status code: 204")

	} else if contentType := resp.Header.Get("Content-Type"); contentType != "application/json" {
		err = ErrorFmt(UnsupportedContentType, "unsupported content-type: '%s' != 'application/json'", contentType)

	} else if jsonErr := json.Unmarshal(resp.Body, obj); err != nil {
		err = &Error{UnmarshalError, jsonErr}
	}

	return
}