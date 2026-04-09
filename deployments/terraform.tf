terraform {
  required_providers {
    tsuru = {
      source  = "tsuru/tsuru"
      version = ">= 2.17.2"
    }
  }
}

provider "tsuru" {
  host = "http://100.64.100.100:8080"
}

resource "tsuru_router" "local-internal-shared-http-lb" {
  name = "local-internal-shared-http-lb-local"
  type = "api"

  config = yamlencode({
    "api-url"       = "http://192.168.105.33:31282/api/ingress"
    "multi-cluster" = false
    "headers" = {
      "Authorization"     = ""
      "X-Router-Instance" = "http"
      "X-Router-Opt" = [
        "domain-suffix=http-apps.local",
        "http-only=true",
        "external-dns.alpha.kubernetes.io/ttl=60",
        "kubernetes.io/ingress.class=nginx"
      ]
    }
  })
}

resource "tsuru_router" "local-http-gateway-router" {
  name = "local-http-gateway-router"
  type = "api"

  config = yamlencode({
    "api-url"       = "http://192.168.105.33:31282/api/gateway-api"
    "multi-cluster" = false
    "headers" = {
      "Authorization"     = ""
      "X-Gateway-Name" = "eg"
      "X-Gateway-Namespace" = "default"
      "X-Router-Instance" = "my-local-gateway"
      "X-Router-Opt" = [
        "domain-suffix=gateway-apps.local",
        "http-only=true",
        "external-dns.alpha.kubernetes.io/ttl=60"
      ]
    }
  })
}