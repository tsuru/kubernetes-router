BINARY=kubernetes-router
TAG=latest
IMAGE=tsuru/$(BINARY)
LOCAL_REGISTRY=10.200.10.1:5000
NAMESPACE=tsuru
LINTER_ARGS = \
	-j 4 --enable-gc -s vendor -e '.*/vendor/.*' --vendor --enable=misspell --enable=gofmt --enable=goimports --enable=unused \
	--disable=gocyclo --disable=gosec --deadline=60m --tests
RUN_FLAGS=-v 9

.PHONY: run
run: build
	./$(BINARY) $(RUN_FLAGS)

.PHONY: build
build:
	go build -o $(BINARY) ./cmd/router

.PHONY: build-docker
build-docker:
	docker build --rm -t $(IMAGE):$(TAG) .

.PHONY: push
push: build-docker
	docker push $(IMAGE):$(TAG)

.PHONY: test
test:
	go test ./... -race -cover

.PHONY: lint
lint:
	go get -u github.com/alecthomas/gometalinter; \
	gometalinter --install; \
	go install  ./...; \
	go test -i ./...; \
	gometalinter $(LINTER_ARGS) ./...; \

.PHONY: minikube
minikube:
	make IMAGE=$(LOCAL_REGISTRY)/$(BINARY) push
	kubectl delete -f deployments/local.yml || true
	cat deployments/rbac.yml | sed 's~NAMESPACE~$(NAMESPACE)~g' | kubectl apply -f -
	cat deployments/local.yml | sed 's~IMAGE~$(LOCAL_REGISTRY)/$(BINARY)~g' | sed 's~NAMESPACE~$(NAMESPACE)~g' | kubectl apply -f -
