module github.com/PlakarKorp/QA/plakar

go 1.26.10

// The targets fuzz the plakar checkout beside QA, not a released version.
replace github.com/PlakarKorp/plakar => ../../plakar

require (
	github.com/PlakarKorp/go-signify v0.1.0
	github.com/PlakarKorp/kloset v1.2.0-alpha.2.0.20261026160819-2efc3399e223
	github.com/PlakarKorp/plakar v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	github.com/spf13/pflag v1.0.10
	github.com/vmihailenco/msgpack/v5 v5.4.1
)

require (
	github.com/PlakarKorp/aes-gcm-siv v0.1.0 // indirect
	github.com/PlakarKorp/go-cdc-chunkers v1.1.0 // indirect
	github.com/PlakarKorp/go-human2duration v0.1.6 // indirect
	github.com/PlakarKorp/integrations/fs v1.1.9 // indirect
	github.com/PlakarKorp/integrations/ptar v1.1.1 // indirect
	github.com/PlakarKorp/pkg v1.1.10 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/ebfe/bcrypt_pbkdf v0.0.0-20140212075826-3c8d2dcb253a // indirect
	github.com/ericlagergren/polyval v0.0.0-20220411101811-e25bc10ba391 // indirect
	github.com/ericlagergren/subtle v0.0.0-20220507045147-890d697da010 // indirect
	github.com/gabriel-vasile/mimetype v1.4.16 // indirect
	github.com/go-git/gcfg v1.5.1-0.20230307220236-3a3c6141e376 // indirect
	github.com/go-git/go-billy/v5 v5.9.2 // indirect
	github.com/go-git/go-git/v5 v5.19.3 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/jbenet/go-context v0.0.0-20150711004518-d14ea06fba99 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/nickball/go-aes-key-wrap v0.0.0-20170929221519-1c3aa3e4dfc5 // indirect
	github.com/pierrec/lz4/v4 v4.1.33 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/cobra v1.10.2 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	github.com/wagslane/go-password-validator v0.3.0 // indirect
	github.com/zeebo/blake3 v0.2.4 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/term v0.47.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	gopkg.in/ini.v1 v1.67.3 // indirect
	gopkg.in/warnings.v0 v0.1.2 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.61.0 // indirect
)
