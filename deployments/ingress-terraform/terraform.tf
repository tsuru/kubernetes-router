provider "kubernetes" {
  config_path    = "~/.kube/config"
  config_context = "minikube"
}

provider "helm" {
  kubernetes {
    config_path    = "~/.kube/config"
    config_context = "minikube"
  }
}

module "ingress-nginx" {
  source = "gcs::https://www.googleapis.com/storage/v1/gglobo-network-tsuru-hub-terraform-modules/ingress-nginx/0.1.1.tar.gz"

  namespace = "tsuru"

  autoscale_min_replicas = 1
  disable_access_logs    = true
}