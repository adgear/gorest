// Copyright (c) 2014 Datacratic. All rights reserved.

package rest

import (
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Mux routes incoming bid requests to the registered routes. Implements
// the http.Handler interface.
//
// The mux currently only supports JSON content-type for regular message
// and text/plain for error messages.
type Mux struct {

	// Root is the path prefix of all the routes to be matched by this
	// mux. Must be set before calling Init and can't be changed
	// afterwards.
	Root string

	// ErrorFunc is called for all errors that passes through this mux. The
	// returned value will overwrite the current error and will be returned to
	// the client instead.. If it's return value is a rest.CodedError then the
	// status code of the error object will be used.
	ErrorFunc func(ErrorType, error) error

	DefaultHandler http.Handler

	initialize sync.Once

	router router
}

// Init initializes the object.
func (mux *Mux) Init() {
	mux.initialize.Do(mux.init)
}

func (mux *Mux) init() {
	if len(mux.Root) == 0 {
		mux.Root = "/"
	} else {
		mux.Root = "/" + strings.Trim(mux.Root, "/")
	}

	if mux.DefaultHandler == nil {
		mux.DefaultHandler = http.DefaultServeMux
	}
}

// AddRoute adds all the given routes to the mux.
func (mux *Mux) AddRoute(routes ...*Route) {
	mux.Init()

	for _, route := range routes {
		mux.router.Add(route)
	}
}

// AddService adds all the routes returned by the Routable objects to the mux.
func (mux *Mux) AddService(routables ...Routable) {
	for _, routable := range routables {
		mux.AddRoute(routable.RESTRoutes()...)
	}
}

func (mux *Mux) route(method, path string) (*Route, []string, error) {
	if strings.HasPrefix(path, mux.Root) {
		sub := path[len(mux.Root):]
		if route, args := mux.router.Route(method, sub); route != nil {
			return route, args, nil
		}
	}

	return nil, nil, fmt.Errorf("unknown path: '%s'", path)
}

func (mux *Mux) respondError(writer http.ResponseWriter, errType ErrorType, code int, err error) {
	if mux.ErrorFunc != nil {
		err = mux.ErrorFunc(errType, err)
	}

	if coded, ok := err.(*CodedError); ok {
		code = coded.Code
		err = coded.Sub
	}

	http.Error(writer, err.Error(), code)
}

// ServeHTTP services incoming HTTP request by routing them to one of the
// registered routes. Handles all marshalling of input and outputs as well as
// any required path parsing.
func (mux *Mux) ServeHTTP(writer http.ResponseWriter, httpReq *http.Request) {
	mux.Init()

	route, args, err := mux.route(httpReq.Method, httpReq.URL.Path)
	if err != nil {
		log.Printf("using default handler for '%s'", httpReq.URL.Path)
		mux.DefaultHandler.ServeHTTP(writer, httpReq)
		return
	}

	if contentType := httpReq.Header.Get("Content-Type"); contentType != "application/json" {
		err := fmt.Errorf("unsupported content type: got '%s' expected 'application/json'", contentType)
		mux.respondError(writer, UnsupportedContentType, http.StatusBadRequest, err)
		return
	}

	body, err := ioutil.ReadAll(httpReq.Body)
	if err != nil {
		mux.respondError(writer, ReadBodyError, http.StatusBadRequest, err)
		return
	}

	resp, restError := route.invoke(args, body)
	if restError != nil {
		mux.respondError(writer, restError.Type, http.StatusBadRequest, restError.Sub)
		return
	}

	if len(resp) == 0 {
		writer.WriteHeader(http.StatusNoContent)
	} else {
		header := writer.Header()
		header.Set("Content-Type", "application/json")
		header.Set("Content-Length", strconv.FormatInt(int64(len(resp)), 10))
		writer.Write(resp)
	}
}

// DefaultMux is the default Mux used by Serve which uses the
// http.DefaultServeMux as the DefaultHandler in Mux if no routes match.
var DefaultMux = new(Mux)

// AddRoute adds a REST route to DefaultMux. See Route type for further details
// on REST route specification.
func AddRoute(path, method string, handler interface{}) {
	DefaultMux.AddRoute(NewRoute(path, method, handler))
}

// AddService adds a Routable service to DefaultMux.
func AddService(routable Routable) {
	DefaultMux.AddService(routable)
}

// Serve is a proxy for the http.Serve function but using the DefaultMux.
func Serve(l net.Listener, mux *Mux) error {
	if mux == nil {
		mux = DefaultMux
	}

	srv := &http.Server{Handler: mux}
	return srv.Serve(l)
}

// ListenAndServe is a proxy for the http.ListenAndServe function but using the
// DefaultMux.
func ListenAndServe(addr string, mux *Mux) error {
	if mux == nil {
		mux = DefaultMux
	}

	server := &http.Server{Addr: addr, Handler: mux}
	return server.ListenAndServe()
}

// ListenAndServeTLS is a proxy for the http.ListenAndServeTLS function but
// using the DefaultMux.
func ListenAndServeTLS(addr string, certFile string, keyFile string, mux *Mux) error {
	if mux == nil {
		mux = DefaultMux
	}

	server := &http.Server{Addr: addr, Handler: mux}
	return server.ListenAndServeTLS(certFile, keyFile)
}