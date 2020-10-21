// Copyright 2020 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package observability

import (
	"net/http"

	"github.com/opentracing/opentracing-go"
	opentracingExt "github.com/opentracing/opentracing-go/ext"
	"github.com/urfave/negroni"
)

func Middleware() negroni.Handler {
	return &middleware{}
}

type middleware struct{}

func (*middleware) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	tracer := opentracing.GlobalTracer()
	tags := []opentracing.StartSpanOption{
		opentracingExt.SpanKindRPCServer,
		opentracing.Tag{Key: "component", Value: "api"},
		opentracing.Tag{Key: "request_id", Value: r.Header.Get("X-Request-ID")},
		opentracing.Tag{Key: "http.method", Value: r.Method},
		opentracing.Tag{Key: "http.url", Value: r.RequestURI},
	}
	wireContext, err := tracer.Extract(
		opentracing.HTTPHeaders,
		opentracing.HTTPHeadersCarrier(r.Header))

	if err == nil {
		tags = append(tags, opentracing.ChildOf(wireContext))
	}
	span := tracer.StartSpan(r.Method, tags...)
	defer span.Finish()
	ctx := opentracing.ContextWithSpan(r.Context(), span)
	newR := r.WithContext(ctx)

	next(rw, newR)
	statusCode := rw.(negroni.ResponseWriter).Status()
	if statusCode == 0 {
		statusCode = 200
	}
	span.SetTag("http.status_code", statusCode)
	if statusCode >= http.StatusInternalServerError {
		opentracingExt.Error.Set(span, true)
	}
}
