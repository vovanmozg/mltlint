.PHONY: test lint build clean

test:
	go test -race -cover ./...

lint:
	golangci-lint run

build:
	CGO_ENABLED=0 go build -o bin/mltlint ./cmd/mltlint

clean:
	rm -rf bin/
