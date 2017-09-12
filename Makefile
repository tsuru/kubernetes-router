BINARY=ingress-router
TAG=latest
IMAGE=tsuru/$(BINARY)
LOCAL_REGISTRY=10.200.10.1:5000
LINTER_ARGS = \
	-j 4 --enable-gc -s vendor -e '.*/vendor/.*' --vendor --enable=misspell --enable=gofmt --enable=goimports --enable=unused \
	--deadline=60m --tests
RUN_FLAGS=-v 9

.PHONY: run
run: build
	./$(BINARY) $(RUN_FLAGS)

.PHONY: build
build:
	go build -o $(BINARY) ./cmd/ingress

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

.PHONY: kube-local
minikube:
	make IMAGE=$(LOCAL_REGISTRY)/ingress-router push
	kubectl delete -f deployments/local.yml || true
	cat deployments/local.yml | sed 's~IMAGE~$(LOCAL_REGISTRY)/ingress-router~g' | kubectl apply -f -