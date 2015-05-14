// Copyright (c) 2014 Datacratic. All rights reserved.

package rest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"sync"
)

// Routable is used to detect objects that are routable by an Endpoint.
type Routable interface {

	// RESTRoutes returns a list of Route that can be used for routing REST
	// requests.
	RESTRoutes() Routes
}

// Routes is convenience type that represents a list of Route objects.
type Routes []*Route

// Route associates a handler which should be invoked for a given HTTP method
// and templated path.
type Route struct {

	// Path is the templated path required by this route. See Handler for the
	// rules related to path.
	Path Path

	// Method represents the HTTP verb required by this route.
	Method string

	// Handler is a function to be invoked whenever for a given HTTP method and
	// templated path.
	//
	// The function can only return at most 2 values where one will be an error
	// object and the other will be the body of the HTTP response.
	//
	// The function needs enough arguments to accept the Path arguments and,
	// optionally, the body of the request. The path arguments will be applied
	// in the same order as the function arguments with the last function
	// argument being the body.
	//
	// If any of the previous rules are broken, Route will panic when Init is
	// called.
	Handler interface{}

	initialize sync.Once

	handler     reflect.Value
	handlerType reflect.Type

	inBody   int
	outBody  int
	outError int
}

// NewRoute creates and initializes a new Route from the method, path and
// handler.
func NewRoute(path, method string, handler interface{}) *Route {
	route := &Route{
		Path:    NewPath(path),
		Method:  method,
		Handler: handler,
	}
	route.Init()
	return route
}

// Init initializes the object.
func (route *Route) Init() {
	route.initialize.Do(route.init)
}

func (route *Route) init() {
	route.handler = reflect.ValueOf(route.Handler)
	route.handlerType = route.handler.Type()

	if route.handlerType.Kind() != reflect.Func {
		log.Panicf("invalid handler type for route { %s %s }: got '%s' expected '%s'",
			route.Method, route.Path, route.handlerType.Kind(), reflect.Func)
	}

	pathArgs := route.Path.NumArgs()
	handlerArgs := route.handlerType.NumIn()

	if pathArgs < handlerArgs-1 {
		log.Panicf("not enough path arguments for route { %s %s }: %d < %d",
			route.Method, route.Path, pathArgs, handlerArgs-1)

	} else if pathArgs > handlerArgs {
		log.Panicf("too many path arguments for route { %s %s }: %d > %d",
			route.Method, route.Path, pathArgs, handlerArgs)

	} else if pathArgs < handlerArgs {
		route.inBody = handlerArgs
	}

	if route.handlerType.NumOut() > 2 {
		log.Panicf("too many return arguments for route %s", route)
	}

	route.outBody = -1
	route.outError = -1

	for i := 0; i < route.handlerType.NumOut(); i++ {
		errorType := reflect.TypeOf((*error)(nil)).Elem()

		if out := route.handlerType.Out(i); out == errorType {
			if route.outError >= 0 {
				log.Panicf("too many error return for route %s", route)
			}
			route.outError = i

		} else {
			if route.outBody >= 0 {
				log.Panicf("too many normal return for route %s", route)
			}
			route.outBody = i
		}
	}
}

func (route *Route) parseArg(data string, value reflect.Value) (err error) {
	switch value.Kind() {

	case reflect.String:
		value.SetString(data)

	case reflect.Bool:
		var b bool
		if b, err = strconv.ParseBool(data); err == nil {
			value.SetBool(b)
		}

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var i int64
		if i, err = strconv.ParseInt(data, 10, value.Type().Bits()); err == nil {
			value.SetInt(i)
		}

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var u uint64
		if u, err = strconv.ParseUint(data, 10, value.Type().Bits()); err == nil {
			value.SetUint(u)
		}

	case reflect.Float32, reflect.Float64:
		var f float64
		if f, err = strconv.ParseFloat(data, value.Type().Bits()); err == nil {
			value.SetFloat(f)
		}

	default:
		err = fmt.Errorf("unsupported argument type for route '%s %s': %s",
			route.Method, route.Path, value.Kind())
	}

	return
}

func (route *Route) isNil(obj reflect.Value) bool {
	switch obj.Kind() {

	case reflect.String:
		return obj.Len() == 0

	case reflect.Chan, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return obj.IsNil()

	default:
		return false

	}
}

func (route *Route) invoke(args []string, body []byte) ([]byte, *Error) {
	var err error
	var in []reflect.Value

	for i := 0; i < route.handlerType.NumIn(); i++ {
		arg := reflect.New(route.handlerType.In(i))

		if i < len(args) {
			err = route.parseArg(args[i], arg.Elem())
		} else {
			buf := bytes.NewReader(body)
			err = json.NewDecoder(buf).Decode(arg.Interface())
		}

		if err != nil {
			return nil, &Error{UnmarshalError, err}
		}

		in = append(in, arg.Elem())
	}

	out := route.handler.Call(in)

	if route.outError >= 0 && !out[route.outError].IsNil() {
		err := out[route.outError].Interface().(error)
		return nil, &Error{HandlerError, err}
	}

	ret := new(bytes.Buffer)

	if route.outBody >= 0 && !route.isNil(out[route.outBody]) {
		bufWriter := bufio.NewWriter(ret)
		if err = json.NewEncoder(bufWriter).Encode(out[route.outBody].Interface()); err != nil {
			return nil, &Error{MarshalError, err}
		}
		bufWriter.Flush()
	}

	return ret.Bytes(), nil
}

// String returns a string represenation of the object suitable for debugging.
func (route *Route) String() string {
	return fmt.Sprintf("{ %s %s }", route.Method, route.Path)
}
