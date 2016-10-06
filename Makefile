all: build

GO ?= go
TEMPDIR:=$(shell mktemp -d)
VERSION:=$(shell sh -c 'grep "const VERSION" main/elock.go  | cut -d\" -f2')
NAME:=$(shell sh -c 'grep "const APP" main/elock.go  | cut -d\" -f2')

export GOPATH := ${TEMPDIR}

build:
	rm -rf ${TEMPDIR}/src
	mkdir -p ${TEMPDIR}/src/github.com/lomik/
	ln -s $(CURDIR) ${TEMPDIR}/src/github.com/lomik/elock
	$(GO) build -o bin/elock main/elock.go

gox-build:
	rm -rf ${TEMPDIR}/src
	mkdir -p ${TEMPDIR}/src/github.com/lomik/
	ln -s $(CURDIR) ${TEMPDIR}/src/github.com/lomik/elock
	mkdir out/
	gox -os="linux" -arch="amd64" -arch="386" -output="out/elock-{{.OS}}-{{.Arch}}"  github.com/lomik/elock/main
	ls -la out/
	mkdir -p out/root/etc/elock/
	./out/elock-linux-amd64 -config-print-default > out/root/etc/elock/elock.json

fpm-deb:
	make fpm-build-deb ARCH=amd64
	make fpm-build-deb ARCH=386
fpm-rpm:
	make fpm-build-rpm ARCH=amd64
	make fpm-build-rpm ARCH=386

fpm-build-deb:
	fpm -s dir -t deb -n $(NAME) -v $(VERSION) \
		--deb-priority optional --category admin \
		--force \
		--deb-compression bzip2 \
		--url https://github.com/lomik/elock \
		--description "Distributed lock backed by etcd" \
		-m "Roman Lomonosov <r.lomonosov@gmail.com>" \
		--license "MIT" \
		-a $(ARCH) \
		--config-files /etc/elock/elock.json \
		out/elock-linux-$(ARCH)=/usr/bin/elock \
		out/root/=/


fpm-build-rpm:
	fpm -s dir -t rpm -n $(NAME) -v $(VERSION) \
		--force \
		--rpm-compression bzip2 --rpm-os linux \
		--url https://github.com/lomik/elock \
		--description "Distributed lock backed by etcd" \
		-m "Roman Lomonosov <r.lomonosov@gmail.com>" \
		--license "MIT" \
		-a $(ARCH) \
		--config-files /etc/elock/elock.json \
		out/elock-linux-$(ARCH)=/usr/bin/elock \
		out/root/=/