# go-makefile agent notes

Consumers fetch `go.mk` and its helper files from `main` at build time, so any
change here ships to every consumer's next build the moment it lands on `main`.
