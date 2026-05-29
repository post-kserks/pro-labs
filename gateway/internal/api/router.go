package api

import (
	"net/http"
	"strings"
)

// Params holds matched path parameters (e.g. ":id" -> "42").
type Params map[string]string

// HandlerFunc is a route handler with access to path params.
type HandlerFunc func(w http.ResponseWriter, r *http.Request, ps Params)

type route struct {
	method string
	segs   []string
	h      HandlerFunc
}

// Router is a tiny method+path-parameter router (no external deps).
type Router struct {
	routes []route
}

func NewRouter() *Router { return &Router{} }

// Handle registers a route. Patterns use ":name" for path params,
// e.g. "/api/v1/patients/:id/visits".
func (rt *Router) Handle(method, pattern string, h HandlerFunc) {
	rt.routes = append(rt.routes, route{
		method: method,
		segs:   splitPath(pattern),
		h:      h,
	})
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		writeCORS(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeCORS(w)

	reqSegs := splitPath(r.URL.Path)
	methodMismatch := false

	for _, rt := range rt.routes {
		ps, ok := match(rt.segs, reqSegs)
		if !ok {
			continue
		}
		if rt.method != r.Method {
			methodMismatch = true
			continue
		}
		rt.h(w, r, ps)
		return
	}

	if methodMismatch {
		WriteError(w, http.StatusMethodNotAllowed, "INVALID_REQUEST", "method not allowed")
		return
	}
	WriteError(w, http.StatusNotFound, "NOT_FOUND", "route not found")
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func match(pattern, actual []string) (Params, bool) {
	if len(pattern) != len(actual) {
		return nil, false
	}
	var ps Params
	for i, seg := range pattern {
		if strings.HasPrefix(seg, ":") {
			if ps == nil {
				ps = Params{}
			}
			ps[seg[1:]] = actual[i]
			continue
		}
		if seg != actual[i] {
			return nil, false
		}
	}
	return ps, true
}

func writeCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}
