

seed/linux-x86_64-seed.tar.gz:
	./seed/build.sh

test: seed go_test \
	simple drv_test bramble_tests \
	test_integration

go_test: install
	go test -race -v ./...
	go test -run="(TestIntegration|TestRunAlmostAllPublicFunctions)"unique -v ./...

# just use LICENSE as a file we can harmlessly "touch" and use as a cache marker
LICENSE: main.go pkg/*/*.go
	go install
	touch LICENSE

install:  LICENSE build_setuid

bramble_tests: install
	bramble test

docker_reptar: ## Used to compare reptar output to gnutar
	cd pkg/reptar && docker build -t reptar . \
	&& docker run -it reptar sh


drv_test: install
	bramble test tests/derivation_test.bramble

touch_file: install
	bramble run lib/busybox:touch_file

simple: install
	bramble run tests/simple/simple:simple

simple2: install
	bramble run tests/simple/simple:simple2

nested: install
	bramble run tests/nested-sources/another-folder/nested:nested

ldd: install
	bramble run lib/seed:ldd

bramble: install
	bramble

repl: install
	bramble repl

gc: install
	bramble gc

go: install
	bramble run lib/go:go

delete_store:
	rm -rf ~/bramble

test_integration: install
	go test -v -run=TestIntegration ./pkg/bramble/

nix_seed: install
	bramble run lib/nix-seed:stdenv

seed: install
	bramble run lib/seed:seed

all_bramble: install
	bramble run all:all

install_reptar:
	cd pkg/reptar/reptar && go install

busybox_sh: install
	bramble run ./tests/busybox:busybox

build_setuid:
	env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
		go build -tags netgo -ldflags '-w' ./pkg/cmd/bramble-setuid
	sudo chown root:root ./bramble-setuid
	sudo chmod u+s,g+s ./bramble-setuid
	rm -f $$(go env GOPATH)/bin/bramble-setuid || true
	mv ./bramble-setuid $$(go env GOPATH)/bin

run_thorn: build_thorn
	go run ./pkg/cmd/thorn
