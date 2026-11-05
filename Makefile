.PHONY: build test vet fmt tidy clean

build:
	./build.sh local

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
