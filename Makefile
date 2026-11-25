BIN := bin

.PHONY: build test vet fmt clean test-userns hooks

build:
	go build -o $(BIN)/ciano .

test:
	go test ./...

test-userns:
	./hack/test-userns.sh

hooks:
	git config core.hooksPath .githooks

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -f $(BIN)/ciano
