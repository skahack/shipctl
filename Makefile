NAME     := shipctl
VERSION  := v0.5.0
REVISION := $(shell git rev-parse --short HEAD)

SRCS      := $(shell find . -type f -name '*.go')
LDFLAGS   := -ldflags="-s -w -X \"main.Version=$(VERSION)\" -X \"main.Revision=$(REVISION)\" -extldflags \"-static\""
DIST_DIRS := find * -type d -exec

bin/$(NAME): $(SRCS)
	@go build -a -tags netgo -installsuffix netgo $(LDFLAGS) -o bin/$(NAME)

.PHONY: deps
deps:
	glide install

.PHONY: install
install:
	go install $(LDFLAGS)

.PHONY: clean
clean:
	rm -rf bin
	rm -rf vendor/*
	rm -rf dist

.PHONY: build-all
build-all:
	gox -verbose \
	$(LDFLAGS) \
	-os="linux darwin" \
	-arch="amd64 386 armv5 armv6 armv7 arm64" \
	-osarch="!darwin/arm64" \
	-output="dist/{{.OS}}-{{.Arch}}/{{.Dir}}" .

.PHONY: dist
dist: build-all
	mkdir -p dist
	cd dist && \
	$(DIST_DIRS) cp ../LICENSE {} \; && \
	$(DIST_DIRS) cp ../README.md {} \; && \
	$(DIST_DIRS) tar -zcf $(NAME)-$(VERSION)-{}.tar.gz {} \; && \
	$(DIST_DIRS) zip -r $(NAME)-$(VERSION)-{}.zip {} \; && \
	cd ..

.PHONY: test
test:
	@go test $$(go list ./... | grep -v '/vendor/') -cover
