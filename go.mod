module criticalsys.net/dirpoller

go 1.26.0

require (
	github.com/fsnotify/fsnotify v1.9.0
	github.com/google/uuid v1.6.0
	github.com/klauspost/compress v1.18.5
	github.com/pkg/sftp v1.13.10
	github.com/zeebo/xxh3 v1.1.0
	golang.org/x/crypto v0.49.0
	golang.org/x/sys v0.42.0
)

require github.com/klauspost/cpuid/v2 v2.3.0 // indirect

require (
	criticalsys/secretprotector v0.0.0
	github.com/kr/fs v0.1.0 // indirect
)

replace criticalsys/secretprotector => ../secretprotector
