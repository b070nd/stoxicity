VERSION ?= dev
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build test vet fmt tidy clean

build:
	cd app && go build -ldflags "$(LDFLAGS)" -o ../bin/stoxicity ./src/cmd/stoxicity
	cd app && go build -ldflags "$(LDFLAGS)" -o ../bin/stoxicity-tui ./src/cmd/stoxicity-tui

test:
	cd app && go test ./...

vet:
	cd app && go vet ./...

fmt:
	cd app && gofmt -l -w .

tidy:
	cd app && go mod tidy

clean:
	rm -rf bin dist
