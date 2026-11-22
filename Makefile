.PHONY: build test race clean

build:
	go build ./cmd/clipwrapd

test:
	go test ./...

race:
	go test -race ./...

clean:
	go clean -cache
