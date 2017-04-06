NAME     := shipctl
VERSION  := v0.1.0
REVISION := $(shell git rev-parse --short HEAD)

SRCS    := $(shell find . -type f -name '*.go')
LDFLAGS := -ldflags="-s -w -X \"main.Version=$(VERSION)\" -X \"main.Revision=$(REVISION)\" -extldflags \"-static\""

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

DIST_DIRS := find ./ -type d -exec
.PHONY: dist
dist: bin/${NAME}
	mkdir -p dist
	cd bin && \
	$(DIST_DIRS) tar -zcf ../dist/$(NAME)-$(VERSION).tar.gz {} \; && \
	$(DIST_DIRS) zip -r ../dist/$(NAME)-$(VERSION).zip {} \; && \
	cd ..

.PHONY: test
test:
	@go test $$(go list ./... | grep -v '/vendor/') -cover
