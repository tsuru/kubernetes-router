BINARY=ingress-router
TAG=latest
IMAGE=tsuru/$(BINARY)

.PHONY: run
run: build
	./$(BINARY)

.PHONY: build
build:
	go build -o $(BINARY)

.PHONY: build-docker
build-docker:
	docker build --rm -t $(IMAGE):$(TAG) .