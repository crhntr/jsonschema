module github.com/crhntr/jsonschema/examples/issues

go 1.27.0

replace github.com/crhntr/jsonschema => ../..

tool (
	github.com/crhntr/jsonschema/cmd/jsch
	github.com/typelate/muxt/cmd/muxt
)

require (
	github.com/crhntr/jsonschema v0.0.0-00010101000000-000000000000
	github.com/typelate/dom v0.10.0
	modernc.org/sqlite v1.60.1
)

require (
	github.com/andybalholm/cascadia v1.3.5 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/ettle/strcase v0.2.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/maxbrunsfeld/counterfeiter/v6 v6.14.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/typelate/check v0.8.1 // indirect
	github.com/typelate/muxt v0.21.0-dev.1 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)
