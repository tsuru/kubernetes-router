
.PHONY: run
run: install
	ingress-router

.PHONY: install
install:
	go install ./...