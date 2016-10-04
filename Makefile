all: bin/elock

GO ?= go
TEMPDIR:=$(shell mktemp -d)
export GOPATH := ${TEMPDIR}

bin/elock:
	rm -rf ${TEMPDIR}/src
	mkdir -p ${TEMPDIR}/src/github.com/lomik/
	ln -s $(CURDIR) ${TEMPDIR}/src/github.com/lomik/elock
	$(GO) build -o bin/elock main/elock.go
