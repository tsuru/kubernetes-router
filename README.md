# Kubernetes Router

Kubernetes router implements the tsuru router http API and manages the creation and removal of
load balancer services or ingress resources on a kubernetes cluster. It expects to be run as a pod in the cluster itself.

## Flags

- `-alsologtostderr`: log to standard error as well as files;
- `-cert-file`: Path to certificate used to serve https requests;
- `-controller-modes`: Defines enabled controller running modes: service, ingress, ingress-nginx or istio-gateway;
- `-ingress-domain`: Default domain to be used on created vhosts, local is the default. (eg: serviceName.local) (default "local");
- `-istio-gateway.gateway-selector`: Gateway selector used in gateways created for apps;
- `-k8s-annotations`: Annotations to be added to each resource created. Expects KEY=VALUE format;
- `-k8s-labels`: Labels to be added to each resource created. Expects KEY=VALUE format;
- `-k8s-namespace`: Kubernetes namespace to create resources (default "default");
- `-k8s-timeout`: Kubernetes per-request timeout (default 10s);
- `-key-file`: Path to private key used to serve https requests;
- `-listen-addr`: Listen address (default ":8077");
- `-log_backtrace_at`: when logging hits line file:N, emit a stack trace;
- `-log_dir`: If non-empty, write log files in this directory;
- `-logtostderr`: log to standard error instead of files;
- `-opts-to-label`: Mapping between router options and service labels. Expects KEY=VALUE format;
- `-opts-to-label-doc`: Mapping between router options and user friendly help. Expects KEY=VALUE format;
- `-pool-labels`: Default labels for a given pool. Expects POOL={"LABEL":"VALUE"} format;
- `-stderrthreshold`: logs at or above this threshold go to stderr;
- `-v`: log level for V logs;
- `-vmodule`: comma-separated list of pattern=N settings for file-filtered logging.

## Envs

- `ROUTER_API_USER`/`ROUTER_API_PASSWORD`: Basic auth user and password to be checked for every request to the router API. Optional.

## Running locally with Tsuru and Minikube

1. Setup tsuru + minikube (https://docs.tsuru.io/master/contributing/compose.html)

2. Run the ingress-router in your minikube cluster

        $ make minikube

3. Fetch the URL of the ingress-router service

        $ minikube service list

4. Add the ingress router to tsuru.conf

        $ vim ../tsuru/etc/tsuru-compose.conf

    Add:

        routers:
            ingress-router:
                type: api
                api-url: http://192.168.99.100:31647

    Replace http://192.168.99.100:31647 with the URL shown by `minikube service list`.

5. Reload tsuru

        $ $GOPATH/src/github.com/tsuru/tsuru/build-compose.sh

6. Create an app using the ingress-router

        $ tsuru app-create myapp static --router ingress-router

