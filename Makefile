
USER_GH=go-i2p
VERSION=0.32.9
packagename=gosam

echo: fmt
	@echo "type make version to do release $(VERSION)"

version:
	github-release release -s $(GITHUB_TOKEN) -u $(USER_GH) -r $(packagename) -t v$(VERSION) -d "version $(VERSION)"

del:
	github-release delete -s $(GITHUB_TOKEN) -u $(USER_GH) -r $(packagename) -t v$(VERSION)

tar:
	tar --exclude .git \
		--exclude .go \
		--exclude bin \
		--exclude examples \
		-cJvf ../$(packagename)_$(VERSION).orig.tar.xz .

link:
	rm -f ../goSam
	ln -sf . ../goSam

fmt:
	gofmt -w -s *.go */*.go
