# Internal prerequisites used across modules. Leading-underscore recipes
# are not surfaced in `just --list`; they're plumbing.

_go-cache:
	mkdir -p {{gocache}}

_ui-deps:
	test -d {{ui_canary}} || (echo "UI dependencies are out of date. Run 'just ui-install' first." && exit 1)

_website-deps:
	test -d {{website_canary}} || (echo "Website dependencies are out of date. Run 'just website-install' first." && exit 1)
