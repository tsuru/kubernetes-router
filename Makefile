BINARY=kubernetes-router
TAG=latest
IMAGE=tsuru/$(BINARY)
LOCAL_REGISTRY=100.64.100.100:5000
NAMESPACE=tsuru-system
LINTER_ARGS = \
	-j 4 --enable-gc -s vendor -e '.*/vendor/.*' --vendor --enable=misspell --enable=gofmt --enable=goimports \
	--disable=gocyclo --disable=gosec --deadline=60m --tests
RUN_FLAGS=-v 9

# When using Podman, set DOCKER=podman
DOCKER ?= docker

.PHONY: run
run: build
	./$(BINARY) $(RUN_FLAGS)

.PHONY: build
build:
	go build -o $(BINARY) ./cmd/router

.PHONY: build-docker
build-docker:
	$(DOCKER) build --rm -t $(IMAGE):$(TAG) .

.PHONY: push
push: build-docker
	# $(DOCKER) push $(IMAGE):$(TAG) --tls-verify=false
	$(DOCKER) push $(IMAGE):$(TAG)

.PHONY: test
test:
	go test ./... -race -cover

.PHONY: lint
lint:
	curl -sfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $$(go env GOPATH)/bin
	$$(go env GOPATH)/bin/golangci-lint run -c ./.golangci.yml ./...

.PHONY: minikube
minikube:
	make IMAGE=$(LOCAL_REGISTRY)/$(BINARY) push
	kubectl delete -f deployments/local.yml || true
	cat deployments/rbac.yml | sed 's~NAMESPACE~$(NAMESPACE)~g' | kubectl apply -f -
	cat deployments/local.yml | sed 's~IMAGE~$(LOCAL_REGISTRY)/$(BINARY)~g' | sed 's~NAMESPACE~$(NAMESPACE)~g' | kubectl apply -f -
