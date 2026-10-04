.PHONY: build test vet fmt check clean

all: check build

build:
	go build -ldflags="-s -w" -o bin/cypher-mcp ./cmd/cypher-mcp

test:
	go test -v -cover ./...

vet:
	go vet ./...

fmt:
	gofmt -w -s .

check: fmt vet test

clean:
	rm -rf bin/ dist/
