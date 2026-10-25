BIN := bin

.PHONY: build test vet fmt clean

build:
	go build -o $(BIN)/ciano .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -f $(BIN)/ciano
