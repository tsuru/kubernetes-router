module github.com/tsuru/kubernetes-router

go 1.14

require (
	github.com/ghodss/yaml v1.0.0
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/gorilla/mux v1.8.0
	github.com/onsi/gomega v1.10.2 // indirect
	github.com/opentracing-contrib/go-stdlib v1.0.0
	github.com/opentracing/opentracing-go v1.2.0
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.11.1
	github.com/stretchr/testify v1.7.0
	github.com/tsuru/tsuru v0.0.0-20201016203419-9a2686f0f674
	github.com/uber/jaeger-client-go v2.25.0+incompatible
	github.com/urfave/negroni v0.2.0
	golang.org/x/net v0.0.0-20220425223048-2871e0cb64e4 // indirect
	golang.org/x/oauth2 v0.0.0-20220411215720-9780585627b5 // indirect
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/protobuf v1.28.0 // indirect
	istio.io/api v0.0.0-20200911191701-0dc35ad5c478
	istio.io/client-go v0.0.0-20200807182027-d287a5abb594
	istio.io/gogo-genproto v0.0.0-20201015184601-1e80d26d6249 // indirect
	istio.io/pkg v0.0.0-20201020203611-6565bf4f242a
	k8s.io/api v0.22.9
	k8s.io/apiextensions-apiserver v0.22.9
	k8s.io/apimachinery v0.22.9
	k8s.io/client-go v0.22.9
)
