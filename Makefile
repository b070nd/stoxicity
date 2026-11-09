.PHONY: build test vet fmt tidy clean hooks guard

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

# turn on the hooks that refuse to commit or push real data (once per clone)
hooks:
	git config core.hooksPath .githooks

# run the data guard over everything not yet on a remote
guard:
	.githooks/guard-no-data.sh range HEAD --not --remotes
