# Kubernetes Router

Kubernetes router implements the tsuru router http API and manages the creation and removal of
load balancer services or ingress resources on a kubernetes cluster. It expects to be run as a pod in the cluster itself.

## Flags

- `-k8s-labels`: Labels to be added to each resource created, expects the format KEY=VALUE;
- `-k8s-annotations`: Annotations to be added to each resource created, expects the format KEY=VALUE;
- `-k8s-namespace`: the namespace on which the resources should be created;
- `-k8s-timeout`: Per request kubernetes timeout;
- `-listen-addr`: The address on which this API should listen;
- `-ingress-mode`: Create ingresses instead of LoadBalancer services;
- `-key-file`: Path to private key used to serve https requests;
- `-cert-file`: Path to certificate used to serve https requests;
- `-opts-to-label`: Router opts to be parsed and turned into service labels, expects the format KEY=VALUE.

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

