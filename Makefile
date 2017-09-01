BINARY=ingress-router
TAG=latest
IMAGE=tsuru/$(BINARY)
LINTER_ARGS = \
	-j 4 --enable-gc -s vendor -e '.*/vendor/.*' --vendor --enable=misspell --enable=gofmt --enable=goimports --enable=unused \
	--deadline=60m --tests

.PHONY: run
run: build
	./$(BINARY)

.PHONY: build
build:
	go build -o $(BINARY)

.PHONY: build-docker
build-docker:
	docker build --rm -t $(IMAGE):$(TAG) .

.PHONY: push
push: build-docker
	docker push $(IMAGE):$(TAG)

.PHONY: test
test:
	go test ./... -race

.PHONY: lint
lint: 
	go get -u github.com/alecthomas/gometalinter; \
	gometalinter --install; \
	go install  ./...; \
	go test -i ./...; \
	gometalinter $(LINTER_ARGS) ./...; \