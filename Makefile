NAME     := ship
VERSION  := v0.0.1
REVISION := $(shell git rev-parse --short HEAD)

SRCS    := $(shell find . -type f -name '*.go')
LDFLAGS := -ldflags="-s -w -X \"main.Version=$(VERSION)\" -X \"main.Revision=$(REVISION)\" -extldflags \"-static\""

bin/$(NAME): $(SRCS)
	@go build -a -tags netgo -installsuffix netgo $(LDFLAGS) -o bin/$(NAME)

dep:
ifeq ($(shell command -v dep 2> /dev/null),)
	go get -u github.com/golang/dep/...
endif

deps: dep
	dep ensure

install:
	go install $(LDFLAGS)

clean:
	rm -rf bin
	rm -rf vendor/*
	rm -rf dist

DIST_DIRS := find ./ -type d -exec
dist: bin/${NAME}
	mkdir -p dist
	cd bin && \
	$(DIST_DIRS) tar -zcf ../dist/$(NAME)-$(VERSION).tar.gz {} \; && \
	$(DIST_DIRS) zip -r ../dist/$(NAME)-$(VERSION).zip {} \; && \
	cd ..

.PHONY: deps clean install dist
